package extension

import (
	"fmt"
	"os"
	"path/filepath"
)

// The runner is the Node side of a warm plugin: it imports the module
// once, then answers JSONL requests on stdin with JSONL replies on
// stdout. Each pool writes it to its own temp directory once and removes
// it when the pool closes, so the wiring is a real file that can be read
// and tested rather than a shell-escaped -e string.

// runnerSource is the script. Keep it plain JavaScript: it runs under
// whatever Node the machine has (24+ strips the plugin's TypeScript
// itself).
const runnerSource = `import { pathToFileURL } from 'node:url';
import { createInterface } from 'node:readline';

// stdout carries protocol only; anything the plugin logs goes to stderr.
for (const level of ['log', 'info', 'debug', 'warn']) {
  console[level] = (...args) => console.error(...args);
}

const entry = process.argv[2];
const pluginDir = process.argv[3] || entry.replace(/[\\/][^\\/]*$/, '');
const write = (obj) => process.stdout.write(JSON.stringify(obj) + '\n');
const message = (error) => String(error && error.stack ? error.stack : error);
const reply = (id, ok, value) => {
  if (ok) write({ id, ok: true, result: value === undefined ? null : value });
  else write({ id, ok: false, error: message(value) });
};

let mod;
try {
  mod = (await import(pathToFileURL(entry).href)).default;
  if (!mod || !Array.isArray(mod.tools)) throw new Error('default export needs a tools array');
  for (const tool of mod.tools) {
    if (!tool || typeof tool.name !== 'string' || typeof tool.execute !== 'function') {
      throw new Error('every tool needs a name and an execute function');
    }
  }
  if (typeof mod.onActivate === 'function') {
    await mod.onActivate({ pluginDir, pid: process.pid, workspace: process.env.PALADIN_WORKSPACE || '' });
  }
} catch (error) {
  console.error('plugin failed to load: ' + message(error));
  process.exit(2);
}

const tools = new Map(mod.tools.map((tool) => [tool.name, tool]));

async function handle(line) {
  let req;
  try {
    req = JSON.parse(line);
  } catch (error) {
    console.error('bad request line: ' + message(error));
    return;
  }
  if (!req || typeof req.op !== 'string') {
    reply(req && req.id, false, 'request needs an op');
    return;
  }
  if (req.op === 'ping') {
    reply(req.id, true, { pid: process.pid });
    return;
  }
  if (req.op === 'list') {
    reply(req.id, true, mod.tools.map(({ name, description, parameters }) => ({ name, description, parameters })));
    return;
  }
  if (req.op === 'shutdown') {
    reply(req.id, true, null);
    try {
      if (typeof mod.onDeactivate === 'function') await mod.onDeactivate();
    } catch (error) {
      console.error('onDeactivate failed: ' + message(error));
    }
    process.exit(0);
  }
  if (req.op !== 'call') {
    reply(req.id, false, 'unknown op ' + req.op);
    return;
  }
  const tool = tools.get(req.tool);
  if (!tool) {
    reply(req.id, false, 'tool not found: ' + req.tool);
    return;
  }
  try {
    const input = req.input || {};
    const value = await tool.execute(input.args, input.context || {});
    reply(req.id, true, typeof value === 'string' ? value : JSON.stringify(value === undefined ? null : value));
  } catch (error) {
    // One failing call never takes the plugin down.
    reply(req.id, false, message(error));
  }
}

// Requests can overlap; the host bounds the number of in-flight calls.
const lines = createInterface({ input: process.stdin, crlfDelay: Infinity });
const pending = new Set();
lines.on('line', (line) => {
  if (!line.trim()) return;
  const task = handle(line);
  pending.add(task);
  task.finally(() => pending.delete(task));
});
lines.on('close', () => {
  Promise.allSettled([...pending]).then(() => process.exit(0));
});
process.stdout.on('error', () => process.exit(0));
`

// runnerScript writes the script for this pool on first use.
func (p *Pool) runnerScript() (string, error) {
	p.scriptOnce.Do(func() {
		dir, err := os.MkdirTemp("", "paladin-ext-")
		if err != nil {
			p.scriptErr = err
			return
		}
		path := filepath.Join(dir, "runner.mjs")
		if err := os.WriteFile(path, []byte(runnerSource), 0o600); err != nil {
			p.scriptErr = err
			return
		}
		if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"type":"module"}`), 0o600); err != nil {
			p.scriptErr = err
			return
		}
		p.scriptDir, p.scriptPath = dir, path
	})
	if p.scriptErr != nil {
		return "", fmt.Errorf("extension runner script: %w", p.scriptErr)
	}
	return p.scriptPath, nil
}

// cleanRunnerScript removes this pool's temp directory.
func (p *Pool) cleanRunnerScript() {
	if p.scriptDir != "" {
		_ = os.RemoveAll(p.scriptDir)
		p.scriptDir, p.scriptPath = "", ""
	}
}
