// ui.go validates the optional UI entries a plugin can ship: side-pane
// panels and inline tool-result cards. UI problems are never fatal to a
// plugin's tools: a bad view is reported and skipped, so a plugin still
// works after its UI breaks.

package extension

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	// maxViews and maxCards bound one manifest.
	maxViews = 4
	maxCards = 16
	// maxViewBytes bounds one panel HTML file; maxCardBytes bounds one
	// card script.
	maxViewBytes = 2 << 20
	maxCardBytes = 512 << 10
)

// View permissions a manifest may declare. A view without "invokeTool"
// can only render and read its own files.
const (
	ViewInvokeTool        = "invokeTool"
	ViewInvokeDestructive = "invokeToolDestructive"
	ViewEvents            = "events"
	ViewReadFile          = "readFile"
	ViewOpenExternal      = "openExternal"
)

var viewPerms = map[string]bool{
	ViewInvokeTool:        true,
	ViewInvokeDestructive: true,
	ViewEvents:            true,
	ViewReadFile:          true,
	ViewOpenExternal:      true,
}

// viewID is a panel id: lower-case, short, unique per plugin.
var viewID = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)

// View is one side-pane panel a plugin ships.
type View struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Entry       string   `json:"entry"`
	Mount       string   `json:"mount"`
	Permissions []string `json:"permissions,omitempty"`
}

// Card is one inline renderer for a tool's result.
type Card struct {
	Tool   string `json:"tool"`
	Entry  string `json:"entry"`
	Inline *bool  `json:"inline,omitempty"`
}

// PluginView is one validated panel as pluginViews reports it.
type PluginView struct {
	Plugin      string   `json:"plugin"`
	View        string   `json:"view"`
	Title       string   `json:"title"`
	Mount       string   `json:"mount"`
	Permissions []string `json:"permissions,omitempty"`
	Destructive bool     `json:"destructive,omitempty"`
}

// PluginCard is one validated card renderer. Exposed is the full tool
// name the transcript event carries.
type PluginCard struct {
	Plugin  string `json:"plugin"`
	Tool    string `json:"tool"`
	Exposed string `json:"exposed"`
}

// PluginUI is the pluginViews result: every panel and card from the
// enabled plugins, plus the UI errors that were skipped.
type PluginUI struct {
	Views  []PluginView `json:"views"`
	Cards  []PluginCard `json:"cards"`
	Errors []string     `json:"errors,omitempty"`
}

// validateUI checks the manifest's views and cards. Valid entries are
// kept on the plugin; failures are collected for the caller to report.
func (p *plugin) validateUI() {
	seenViews := map[string]bool{}
	for i, v := range p.Views {
		label := fmt.Sprintf("view %d", i)
		if v.ID != "" {
			label = "view " + v.ID
		}
		if err := p.checkView(v, seenViews); err != nil {
			p.uiErrs = append(p.uiErrs, fmt.Errorf("%s: %w", label, err))
			continue
		}
		p.views = append(p.views, v)
	}
	if len(p.Views) > maxViews {
		p.uiErrs = append(p.uiErrs, fmt.Errorf("at most %d views are allowed", maxViews))
	}
	seenCards := map[string]bool{}
	static := p.staticToolNames()
	for i, c := range p.Cards {
		label := fmt.Sprintf("card %d", i)
		if c.Tool != "" {
			label = "card " + c.Tool
		}
		if err := p.checkCard(c, seenCards, static); err != nil {
			p.uiErrs = append(p.uiErrs, fmt.Errorf("%s: %w", label, err))
			continue
		}
		p.cards = append(p.cards, c)
	}
	if len(p.Cards) > maxCards {
		p.uiErrs = append(p.uiErrs, fmt.Errorf("at most %d cards are allowed", maxCards))
	}
}

func (p *plugin) checkView(v View, seen map[string]bool) error {
	if !viewID.MatchString(v.ID) {
		return fmt.Errorf("id must match %s", viewID)
	}
	if seen[v.ID] {
		return errors.New("duplicate view id")
	}
	seen[v.ID] = true
	title := strings.TrimSpace(v.Title)
	if n := utf8.RuneCountInString(title); n < 1 || n > 48 {
		return errors.New("title must be 1 to 48 characters")
	}
	for _, r := range title {
		if unicode.IsControl(r) {
			return errors.New("title must be plain text")
		}
	}
	mount := strings.TrimSpace(v.Mount)
	if mount == "" {
		mount = "side-pane"
	}
	if mount != "side-pane" {
		return fmt.Errorf("mount must be %q; other mounts are not supported yet", "side-pane")
	}
	if err := p.checkUIEntry(v.Entry, ".html", maxViewBytes); err != nil {
		return err
	}
	for _, perm := range v.Permissions {
		if !viewPerms[perm] {
			return fmt.Errorf("unknown permission %q", perm)
		}
	}
	return nil
}

func (p *plugin) checkCard(c Card, seen map[string]bool, static map[string]bool) error {
	if !toolName.MatchString(c.Tool) {
		return fmt.Errorf("tool %q needs a name matching %s", c.Tool, toolName)
	}
	if seen[c.Tool] {
		return errors.New("duplicate card tool")
	}
	seen[c.Tool] = true
	if c.Inline != nil && !*c.Inline {
		return errors.New("only inline cards are supported")
	}
	if err := p.checkUIEntry(c.Entry, ".js", maxCardBytes); err != nil {
		return err
	}
	if static != nil && !static[c.Tool] {
		return fmt.Errorf("tool %q is not in the manifest's static tools", c.Tool)
	}
	return nil
}

// checkUIEntry requires a regular file under ui/ inside the plugin
// folder, with the given extension and size cap.
func (p *plugin) checkUIEntry(rel, ext string, max int64) error {
	if strings.TrimSpace(rel) == "" {
		return errors.New("entry is required")
	}
	if filepath.IsAbs(rel) || strings.ContainsAny(rel, `\`) {
		return fmt.Errorf("entry must be a relative path, got %q", rel)
	}
	clean := filepath.Clean(rel)
	if clean == "." || strings.HasPrefix(clean, "..") {
		return fmt.Errorf("entry must stay inside the plugin folder, got %q", rel)
	}
	if !strings.HasPrefix(clean, "ui"+string(filepath.Separator)) {
		return fmt.Errorf("entry must be under ui/, got %q", rel)
	}
	if filepath.Ext(clean) != ext {
		return fmt.Errorf("entry must end in %s, got %q", ext, rel)
	}
	path := filepath.Join(p.dir, clean)
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return fmt.Errorf("entry %s: %w", rel, err)
	}
	if !contained(p.dir, real) {
		return fmt.Errorf("entry %s links outside the plugin folder", rel)
	}
	info, err := os.Stat(real)
	if err != nil {
		return fmt.Errorf("entry %s: %w", rel, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("entry %s must be a regular file", rel)
	}
	if info.Size() > max {
		return fmt.Errorf("entry %s exceeds %d bytes", rel, max)
	}
	return nil
}

// staticToolNames returns the names in a static manifest tool list, or
// nil when the plugin lists its tools dynamically.
func (p *plugin) staticToolNames() map[string]bool {
	if len(p.Tools) == 0 || isNull(p.Tools) {
		return nil
	}
	var decls []declaration
	if json.Unmarshal(p.Tools, &decls) != nil {
		return nil
	}
	names := map[string]bool{}
	for _, d := range decls {
		names[d.Name] = true
	}
	return names
}

// UI scans the enabled plugins for panels and cards. It reads manifests
// and checks files, but never starts a runner, so opening the Plugins
// menu costs no plugin process. A broken plugin is reported and skipped.
func (r *Registry) UI() PluginUI {
	out := PluginUI{Views: []PluginView{}, Cards: []PluginCard{}}
	root := filepath.Join(r.dataDir, "config", "extensions")
	configPath := filepath.Join(r.dataDir, "config", "extensions.json")
	data, err := os.ReadFile(configPath)
	if errors.Is(err, os.ErrNotExist) {
		return out
	}
	if err != nil {
		out.Errors = append(out.Errors, err.Error())
		return out
	}
	if len(data) > 64<<10 {
		out.Errors = append(out.Errors, "extensions.json is too large")
		return out
	}
	var cfg configFile
	if err := json.Unmarshal(data, &cfg); err != nil {
		out.Errors = append(out.Errors, err.Error())
		return out
	}
	seen := map[string]bool{}
	for _, name := range cfg.Enabled {
		if seen[name] {
			continue
		}
		seen[name] = true
		if !pluginName.MatchString(name) {
			// A loose legacy file has no manifest and no UI.
			continue
		}
		pl, err := loadManifest(root, name)
		if err != nil {
			out.Errors = append(out.Errors, err.Error())
			continue
		}
		for _, e := range pl.uiErrs {
			out.Errors = append(out.Errors, fmt.Sprintf("plugin %s: %s", name, e))
		}
		for _, v := range pl.views {
			perms := append([]string(nil), v.Permissions...)
			out.Views = append(out.Views, PluginView{
				Plugin: name, View: v.ID, Title: strings.TrimSpace(v.Title),
				Mount: mountOf(v), Permissions: perms, Destructive: destructive(v),
			})
		}
		for _, c := range pl.cards {
			out.Cards = append(out.Cards, PluginCard{
				Plugin: name, Tool: c.Tool, Exposed: "extension__" + name + "__" + c.Tool,
			})
		}
	}
	return out
}

func mountOf(v View) string {
	if strings.TrimSpace(v.Mount) == "" {
		return "side-pane"
	}
	return strings.TrimSpace(v.Mount)
}

func destructive(v View) bool {
	for _, p := range v.Permissions {
		if p == ViewInvokeDestructive {
			return true
		}
	}
	return false
}
