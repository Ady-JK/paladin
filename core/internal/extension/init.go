package extension

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
)

const types = `export type ToolContext = { workspace: string; project: string; sessionId: string };
export type PaladinTool = {
  name: string;
  description: string;
  parameters: { type: "object"; properties?: Record<string, unknown>; required?: string[]; additionalProperties?: boolean };
  execute(args: any, context: ToolContext): string | object | Promise<string | object>;
};
export type PluginContext = { pluginDir: string; pid: number; workspace: string };
export type PaladinExtension = {
  tools: PaladinTool[];
  // Optional: run once when the plugin's process starts and when it stops.
  onActivate?(ctx: PluginContext): void | Promise<void>;
  onDeactivate?(): void | Promise<void>;
};
`

const example = `import type { PaladinExtension } from "./paladin.d.ts";

export default {
  tools: [{
    name: "hello",
    description: "Return a greeting for a given name.",
    parameters: {
      type: "object",
      properties: { name: { type: "string", description: "Name to greet" } },
      required: ["name"],
      additionalProperties: false,
    },
    execute(args: { name: string }) {
      return { greeting: "Hello, " + args.name };
    },
  }],
} satisfies PaladinExtension;
`

// pluginExample is the plugin-folder template that --example-plugin
// scaffolds: a ready folder that needs only its name in extensions.json.
const pluginExample = `import { readFileSync } from "node:fs";
import { join } from "node:path";
import type { PaladinExtension, PluginContext } from "./paladin.d.ts";

let greeting = "Hello";

export default {
  // Runs once, when the plugin's warm process starts.
  onActivate(ctx: PluginContext) {
    try {
      greeting = readFileSync(join(ctx.pluginDir, "greeting.txt"), "utf8").trim() || greeting;
    } catch {
      // No greeting.txt: keep the default.
    }
  },
  tools: [{
    name: "hello",
    description: "Return a greeting for a given name, using greeting.txt when it exists.",
    parameters: {
      type: "object",
      properties: { name: { type: "string", description: "Name to greet" } },
      required: ["name"],
      additionalProperties: false,
    },
    execute(args: { name: string }) {
      return { greeting: greeting + ", " + args.name };
    },
  }],
} satisfies PaladinExtension;
`

// manifestExample documents every manifest field in a working file.
const manifestExample = `{
  "entry": "index.ts",
  "timeoutMs": 120000,
  "idleMs": 300000,
  "permissions": []
}
`

// Init creates a template and types only when each file is absent. The
// example remains disabled until added to extensions.json explicitly.
// With examplePlugin it also scaffolds an enabled-by-hand plugin folder,
// `hello/`, that shows the warm-process lifecycle.
func Init(dataDir string, examplePlugin bool) (string, error) {
	dir := filepath.Join(dataDir, "config", "extensions")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	files := map[string]string{
		filepath.Join(dataDir, "config", "extensions.json"): "{\n  \"enabled\": []\n}\n",
		filepath.Join(dir, "paladin.d.ts"):                  types,
		filepath.Join(dir, "hello.ts"):                      example,
	}
	if examplePlugin {
		files[filepath.Join(dir, "hello", "index.ts")] = pluginExample
		files[filepath.Join(dir, "hello", "paladin-plugin.json")] = manifestExample
		files[filepath.Join(dir, "hello", "paladin.d.ts")] = types
	}
	// A deterministic order keeps the output stable for users diffing it.
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		content := files[path]
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return "", err
		}
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		_, writeErr := file.WriteString(content)
		closeErr := file.Close()
		if writeErr != nil {
			return "", writeErr
		}
		if closeErr != nil {
			return "", closeErr
		}
	}
	return dir, nil
}
