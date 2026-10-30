package mcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// NamedServer is one server parsed from pasted text.
type NamedServer struct {
	Name   string       `json:"name"`
	Config ServerConfig `json:"config"`
}

// ParseSnippet turns pasted text into servers. It accepts the shapes
// people copy from READMEs: a launch command per line ("npx -y pkg",
// "uvx pkg", "docker run …", with optional KEY=value prefixes), a
// Streamable HTTP URL, "claude/codex mcp add …" lines, or mcp.json
// JSON (a full file, a "servers" map, one "name": {…} entry, or a
// single server object).
func ParseSnippet(text string) ([]NamedServer, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, errors.New("paste a command, URL or mcp.json snippet")
	}
	var out []NamedServer
	var err error
	if text[0] == '{' || text[0] == '"' {
		out, err = parseJSONSnippet(text)
	} else {
		out, err = parseCommandLines(text)
	}
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, errors.New("no MCP server found in the pasted text")
	}
	for _, s := range out {
		if err := validateServer(s.Name, s.Config); err != nil {
			return nil, err
		}
	}
	return uniqueNames(out), nil
}

// validateServer checks one entry the way mcp.json loading does.
func validateServer(name string, cfg ServerConfig) error {
	if strings.TrimSpace(name) == "" {
		return errors.New("a server has an empty name")
	}
	if t := strings.ToLower(strings.TrimSpace(cfg.Type)); t == "sse" {
		return fmt.Errorf("server %q: the legacy SSE transport is not supported; use the server's Streamable HTTP endpoint (type \"http\")", name)
	}
	if cfg.Transport() == "" {
		return fmt.Errorf("server %q: set either \"command\" (stdio) or \"url\" (http)", name)
	}
	if cfg.StartupTimeoutSec < 0 || cfg.ToolTimeoutSec < 0 {
		return fmt.Errorf("server %q: timeouts must be positive", name)
	}
	return nil
}

func parseJSONSnippet(text string) ([]NamedServer, error) {
	if text[0] == '"' {
		text = "{" + text + "}"
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal([]byte(text), &doc); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	for _, key := range []string{"mcpServers", "servers"} {
		if raw, ok := doc[key]; ok {
			var inner map[string]json.RawMessage
			if err := json.Unmarshal(raw, &inner); err != nil {
				return nil, fmt.Errorf("invalid JSON: %q must be an object of servers", key)
			}
			return serversFromMap(inner)
		}
	}
	if _, ok := doc["command"]; ok {
		return singleServer(text)
	}
	if _, ok := doc["url"]; ok {
		return singleServer(text)
	}
	return serversFromMap(doc)
}

func singleServer(text string) ([]NamedServer, error) {
	var cfg ServerConfig
	if err := json.Unmarshal([]byte(text), &cfg); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	return []NamedServer{{Name: deriveName(cfg), Config: cfg}}, nil
}

func serversFromMap(doc map[string]json.RawMessage) ([]NamedServer, error) {
	names := make([]string, 0, len(doc))
	for n := range doc {
		names = append(names, n)
	}
	sort.Strings(names)
	var out []NamedServer
	for _, n := range names {
		var cfg ServerConfig
		if err := json.Unmarshal(doc[n], &cfg); err != nil {
			return nil, fmt.Errorf("server %q: %w", n, err)
		}
		out = append(out, NamedServer{Name: n, Config: cfg})
	}
	return out, nil
}

var envAssign = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)

func parseCommandLines(text string) ([]NamedServer, error) {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\\\n", " ")
	var out []NamedServer
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "//") {
			continue
		}
		for _, prompt := range []string{"$ ", "> ", "% "} {
			line = strings.TrimPrefix(line, prompt)
		}
		words, err := splitWords(line)
		if err != nil {
			return nil, err
		}
		s, err := parseCommand(words)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}

func parseCommand(words []string) (NamedServer, error) {
	var env map[string]string
	for len(words) > 0 && envAssign.MatchString(words[0]) {
		k, v, _ := strings.Cut(words[0], "=")
		if env == nil {
			env = map[string]string{}
		}
		env[k] = v
		words = words[1:]
	}
	if len(words) == 0 {
		return NamedServer{}, errors.New("no command to run after the environment variables")
	}
	if len(words) >= 3 && slices.Contains([]string{"claude", "codex", "gemini"}, words[0]) && words[1] == "mcp" && words[2] == "add" {
		s, err := parseAgentAdd(words[3:])
		if err != nil {
			return NamedServer{}, err
		}
		for k, v := range env {
			if s.Config.Env == nil {
				s.Config.Env = map[string]string{}
			}
			s.Config.Env[k] = v
		}
		return s, nil
	}
	if len(words) == 1 && isURL(words[0]) {
		cfg := ServerConfig{Type: TransportHTTP, URL: words[0]}
		return NamedServer{Name: deriveName(cfg), Config: cfg}, nil
	}
	cfg := ServerConfig{Command: words[0], Env: env}
	if len(words) > 1 {
		cfg.Args = words[1:]
	}
	return NamedServer{Name: deriveName(cfg), Config: cfg}, nil
}

// parseAgentAdd reads the arguments of "claude mcp add" and
// "codex mcp add": flags and a name, then a URL or "-- command args".
func parseAgentAdd(words []string) (NamedServer, error) {
	var (
		name, transport string
		positional      []string
		cmd             []string
		env, headers    map[string]string
	)
	value := func(i *int, w string) string {
		if _, v, ok := strings.Cut(w, "="); ok && strings.HasPrefix(w, "--") {
			return v
		}
		if *i+1 < len(words) {
			*i++
			return words[*i]
		}
		return ""
	}
	for i := 0; i < len(words); i++ {
		w := words[i]
		flag, _, _ := strings.Cut(w, "=")
		switch {
		case w == "--":
			cmd = words[i+1:]
			i = len(words)
		case flag == "-e" || flag == "--env":
			k, v, _ := strings.Cut(value(&i, w), "=")
			if env == nil {
				env = map[string]string{}
			}
			env[k] = v
		case flag == "-H" || flag == "--header":
			k, v, _ := strings.Cut(value(&i, w), ":")
			if headers == nil {
				headers = map[string]string{}
			}
			headers[strings.TrimSpace(k)] = strings.TrimSpace(v)
		case flag == "-t" || flag == "--transport":
			transport = strings.ToLower(value(&i, w))
		case flag == "--url":
			positional = append(positional, value(&i, w))
		case flag == "-s" || flag == "--scope":
			value(&i, w)
		case strings.HasPrefix(w, "-"):
		default:
			positional = append(positional, w)
		}
	}
	if transport == "sse" {
		return NamedServer{}, errors.New("the legacy SSE transport is not supported; use the server's Streamable HTTP endpoint")
	}
	if len(positional) > 0 {
		name, positional = positional[0], positional[1:]
	}
	var cfg ServerConfig
	switch {
	case len(cmd) > 0:
		cfg = ServerConfig{Command: cmd[0], Env: env}
		if len(cmd) > 1 {
			cfg.Args = cmd[1:]
		}
	case len(positional) > 0 && isURL(positional[0]):
		cfg = ServerConfig{Type: TransportHTTP, URL: positional[0], Headers: headers}
	case len(positional) > 0:
		cfg = ServerConfig{Command: positional[0], Args: positional[1:], Env: env}
	default:
		return NamedServer{}, errors.New("the mcp add line has no command or URL")
	}
	if name == "" {
		name = deriveName(cfg)
	}
	return NamedServer{Name: name, Config: cfg}, nil
}

func isURL(s string) bool {
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")
}

// splitWords splits a command line like a POSIX shell would for the
// simple cases READMEs use: quotes, backslash escapes, whitespace.
func splitWords(line string) ([]string, error) {
	var (
		words   []string
		cur     strings.Builder
		inWord  bool
		quote   rune
		escaped bool
	)
	for _, r := range line {
		switch {
		case escaped:
			cur.WriteRune(r)
			escaped = false
		case quote == '\'':
			if r == '\'' {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case quote == '"':
			switch r {
			case '"':
				quote = 0
			case '\\':
				escaped = true
			default:
				cur.WriteRune(r)
			}
		case r == '\\':
			escaped, inWord = true, true
		case r == '\'' || r == '"':
			quote, inWord = r, true
		case r == ' ' || r == '\t':
			if inWord {
				words = append(words, cur.String())
				cur.Reset()
				inWord = false
			}
		default:
			cur.WriteRune(r)
			inWord = true
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("unterminated %c quote in %q", quote, line)
	}
	if inWord {
		words = append(words, cur.String())
	}
	return words, nil
}

// valueFlags take the next word as their value in launcher commands.
var valueFlags = map[string]bool{
	"--from": true, "-p": true, "--package": true, "--python": true, "--with": true, "--registry": true,
	"-e": true, "--env": true, "-v": true, "--volume": true, "--name": true, "--network": true,
	"--env-file": true, "-w": true, "--workdir": true, "--entrypoint": true, "-u": true, "--user": true,
	"--mount": true, "--platform": true, "--index-url": true,
}

// deriveName guesses a short server name from what it runs.
func deriveName(cfg ServerConfig) string {
	if cfg.Transport() == TransportHTTP {
		return nameFromURL(cfg.URL)
	}
	base := strings.ToLower(filepath.Base(cfg.Command))
	base = strings.TrimSuffix(strings.TrimSuffix(base, ".exe"), ".cmd")
	args := cfg.Args
	pick := ""
	switch base {
	case "npx", "bunx", "pnpx", "uvx", "pipx", "pnpm", "yarn", "docker", "podman", "deno":
		args = slices.DeleteFunc(slices.Clone(args), func(a string) bool {
			return a == "run" || a == "dlx" || a == "exec"
		})
		pick = firstPlain(args)
	case "node", "python", "python3", "bun", "ruby", "java", "uv":
		for i, a := range args {
			if a == "-m" && i+1 < len(args) {
				pick = args[i+1]
				break
			}
		}
		if pick == "" {
			pick = firstPlain(slices.DeleteFunc(slices.Clone(args), func(a string) bool { return a == "run" }))
		}
	}
	if pick == "" {
		pick = base
	}
	return cleanServerName(pick)
}

func firstPlain(args []string) string {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") {
			if valueFlags[a] {
				i++
			}
			continue
		}
		return a
	}
	return ""
}

var nameJunk = regexp.MustCompile(`[^a-z0-9_-]+`)

func cleanServerName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if strings.HasPrefix(s, "@") {
		if _, rest, ok := strings.Cut(s, "/"); ok {
			s = rest
		}
	}
	if i := strings.LastIndexAny(s, "@:"); i > 0 {
		s = s[:i]
	}
	s = filepath.Base(strings.ReplaceAll(s, "\\", "/"))
	s = strings.TrimSuffix(s, filepath.Ext(s))
	orig := s
	for _, p := range []string{"mcp-server-", "server-", "mcp-"} {
		s = strings.TrimPrefix(s, p)
	}
	for _, x := range []string{"-mcp-server", "-server-mcp", "-mcp", "_mcp", "-server"} {
		s = strings.TrimSuffix(s, x)
	}
	if s == "" {
		s = orig
	}
	s = strings.Trim(nameJunk.ReplaceAllString(s, "-"), "-")
	if s == "" {
		return "server"
	}
	return s
}

func nameFromURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return "server"
	}
	host := strings.ToLower(u.Hostname())
	if host == "localhost" || net.ParseIP(host) != nil {
		segs := strings.FieldsFunc(u.Path, func(r rune) bool { return r == '/' })
		if len(segs) == 0 {
			return "server"
		}
		return cleanServerName(segs[len(segs)-1])
	}
	labels := strings.Split(host, ".")
	if len(labels) > 1 {
		labels = labels[:len(labels)-1]
	}
	labels = slices.DeleteFunc(labels, func(l string) bool {
		return l == "mcp" || l == "api" || l == "www" || l == "server"
	})
	if len(labels) == 0 {
		return "server"
	}
	return cleanServerName(labels[len(labels)-1])
}

func uniqueNames(list []NamedServer) []NamedServer {
	seen := map[string]bool{}
	for i := range list {
		base, name := list[i].Name, list[i].Name
		for n := 2; seen[name]; n++ {
			name = fmt.Sprintf("%s-%d", base, n)
		}
		seen[name] = true
		list[i].Name = name
	}
	return list
}

// AddServers adds servers to one config file and starts them. Existing
// names are refused unless replace is set. A folder file that was
// trusted, or did not exist, is trusted afterwards; one that wasn't
// stays untrusted so adding a server never vouches for others.
func (m *Manager) AddServers(scope, folder string, list []NamedServer, replace bool) error {
	folder = cleanFolder(folder)
	if len(list) == 0 {
		return errors.New("no servers to add")
	}
	for _, s := range list {
		if err := validateServer(s.Name, s.Config); err != nil {
			return err
		}
	}
	path, err := m.path(scope, folder)
	if err != nil {
		return err
	}
	old, err := os.ReadFile(path)
	existed := err == nil
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	doc := map[string]any{}
	if existed && strings.TrimSpace(string(old)) != "" {
		dec := json.NewDecoder(bytes.NewReader(old))
		dec.UseNumber()
		if err := dec.Decode(&doc); err != nil {
			return fmt.Errorf("%s is not valid JSON; fix it first: %w", path, err)
		}
	}
	servers, _ := doc["mcpServers"].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
	}
	for _, s := range list {
		if _, taken := servers[s.Name]; taken && !replace {
			return fmt.Errorf("a server named %q already exists in %s", s.Name, path)
		}
		var entry map[string]any
		raw, _ := json.Marshal(s.Config)
		_ = json.Unmarshal(raw, &entry)
		servers[s.Name] = entry
	}
	doc["mcpServers"] = servers
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return err
	}
	trusted := scope == ScopeGlobal || !existed || m.trust.Trusted(folder, Fingerprint(old))
	if err := m.write(scope, folder, buf.Bytes(), trusted); err != nil {
		return err
	}
	for _, s := range m.active(folder) {
		if s.key.scope == scope && slices.ContainsFunc(list, func(n NamedServer) bool { return n.Name == s.key.name }) {
			s.ensure()
		}
	}
	return nil
}
