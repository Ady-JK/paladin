package extension

import (
	"errors"
	"os"
	"path/filepath"
)

const types = `export type ToolContext = { workspace: string; project: string; sessionId: string };
export type PaladinTool = {
  name: string;
  description: string;
  parameters: { type: "object"; properties?: Record<string, unknown>; required?: string[]; additionalProperties?: boolean };
  execute(args: any, context: ToolContext): string | object | Promise<string | object>;
};
export type PaladinExtension = { tools: PaladinTool[] };
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

// Init creates a template and types only when each file is absent. The
// example remains disabled until added to extensions.json explicitly.
func Init(dataDir string) (string, error) {
	dir := filepath.Join(dataDir, "config", "extensions")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	for path, content := range map[string]string{
		filepath.Join(dataDir, "config", "extensions.json"): "{\n  \"enabled\": []\n}\n",
		filepath.Join(dir, "paladin.d.ts"):                  types,
		filepath.Join(dir, "hello.ts"):                      example,
	} {
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
