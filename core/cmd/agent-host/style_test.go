package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

var camelName = regexp.MustCompile(`^[a-z][a-zA-Z0-9]*$`)

// snakeAllowed lists where snake_case JSON stays on purpose: shapes
// Paladin does not own. Tool arguments and results are the model's
// contract (it reads `job_id` back as input), and provider payloads
// follow each vendor's API.
var snakeAllowed = []string{
	"internal/tools/",
	"internal/inference/",
	"internal/memory/memory.go",   // Op is the memory tool's input
	"internal/agent/loop.go",      // parses the shell tool's receipt
	"internal/session/session.go", // rebuilds shell tool arguments
}

// Paladin-owned JSON (wire payloads, files on disk) is camelCase.
func TestOwnedJSONIsCamelCase(t *testing.T) {
	root := filepath.Join("..", "..")
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		for _, allowed := range snakeAllowed {
			if strings.HasPrefix(rel, allowed) {
				return nil
			}
		}
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(file, func(n ast.Node) bool {
			field, ok := n.(*ast.Field)
			if !ok || field.Tag == nil {
				return true
			}
			raw, _ := strconv.Unquote(field.Tag.Value)
			name, _, _ := strings.Cut(reflect.StructTag(raw).Get("json"), ",")
			if name != "" && name != "-" && !camelName.MatchString(name) {
				t.Errorf("%s: json key %q is not camelCase", fset.Position(field.Pos()), name)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestMethodNamesAreCamelCase(t *testing.T) {
	for name := range methods {
		if !camelName.MatchString(name) {
			t.Errorf("RPC method %q is not camelCase", name)
		}
	}
}
