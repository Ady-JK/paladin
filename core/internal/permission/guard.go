package permission

import (
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// The guard is the only thing standing between the agent and the machine:
// everything runs freely except a short list of catastrophic or
// security-damaging actions, which ask the user once. It reads commands
// the way a person would (quotes, pipes, sudo and bash -c unwrapped) and
// errs towards asking; nothing here is a sandbox.

// Reasons shown on the card, one per kind of risk.
const (
	reasonWipe     = "would delete a very broad or protected location"
	reasonDisk     = "would write to or erase a disk device"
	reasonPorts    = "would expose a local port or service beyond this machine"
	reasonFirewall = "would change firewall rules"
	reasonCreds    = "would read, copy or send credentials or keys"
	reasonEnvSend  = "would send environment variables off this machine"
	reasonPipeSh   = "would run a downloaded script without looking at it"
	reasonPersist  = "would change a login, startup or scheduled-task file"
)

var caseBlindFS = runtime.GOOS == "darwin" || runtime.GOOS == "windows"

// CheckShell reports why a shell command needs the user's go-ahead; none
// means it may run.
func CheckShell(cmd, workspace, home string) []string {
	g := &guard{ws: filepath.Clean(workspace), home: filepath.Clean(home)}
	if workspace == "" {
		g.ws = ""
	}
	if home == "" {
		g.home = ""
	}
	g.command(cmd, 0)
	return g.reasons
}

// CheckPath reports why a file read or write needs the user's go-ahead.
func CheckPath(path string, write bool, home string) []string {
	g := &guard{home: filepath.Clean(home)}
	if home == "" {
		g.home = ""
	}
	p := filepath.Clean(path)
	if g.credential(p) {
		g.add(reasonCreds)
	}
	if write && g.persistence(p) {
		g.add(reasonPersist)
	}
	return g.reasons
}

type guard struct {
	ws, home string
	reasons  []string
	cwdMoved bool // a cd earlier in the command: relative paths no longer mean the workspace
}

func (g *guard) add(reason string) {
	for _, r := range g.reasons {
		if r == reason {
			return
		}
	}
	g.reasons = append(g.reasons, reason)
}

var forkBomb = regexp.MustCompile(`:\s*\(\s*\)\s*\{[^}]*:\s*\|\s*:\s*&`)

func (g *guard) command(cmd string, depth int) {
	if depth > 3 {
		return
	}
	if forkBomb.MatchString(cmd) {
		g.add(reasonDisk)
	}
	pipelines, subs := scan(cmd)
	for _, sub := range subs {
		g.command(sub, depth+1)
	}
	for _, pipe := range pipelines {
		g.pipeline(pipe, depth)
	}
}

type segment struct {
	words   []string
	redirs  []string
	subs    []string
	procSub bool
}

func (g *guard) pipeline(pipe []segment, depth int) {
	var names []string
	for _, seg := range pipe {
		for _, r := range seg.redirs {
			g.redirect(r)
		}
		words := unwrap(seg.words)
		if len(words) == 0 {
			// A bare wrapper ("env", "time") is still a command: env | nc.
			name := ""
			if len(seg.words) > 0 {
				name = commandName(seg.words[0])
			}
			names = append(names, name)
			continue
		}
		name := commandName(words[0])
		names = append(names, name)
		g.segment(name, words[1:], seg, depth)
	}
	g.pipes(pipe, names)
}

// pipes looks across a pipeline: environment into the network, a
// download into a shell.
func (g *guard) pipes(pipe []segment, names []string) {
	for i, name := range names {
		switch {
		case envDumpers[name]:
			for _, later := range names[i+1:] {
				if networkSenders[later] {
					g.add(reasonEnvSend)
				}
			}
		case downloaders[name]:
			for j := i + 1; j < len(names); j++ {
				w := unwrap(pipe[j].words)
				if shells[names[j]] || (interpreters[names[j]] && (len(w) <= 1 || w[1] == "-")) {
					g.add(reasonPipeSh)
				}
			}
		}
	}
}

var (
	shells         = set("sh", "bash", "zsh", "dash", "ksh", "fish")
	interpreters   = set("python", "python3", "perl", "ruby", "node")
	downloaders    = set("curl", "wget", "fetch")
	envDumpers     = set("env", "printenv")
	networkSenders = set("curl", "wget", "nc", "ncat", "netcat", "socat", "ssh", "scp", "telnet")
	viewers        = set("cat", "less", "more", "head", "tail", "grep", "rg", "ls", "stat", "file", "wc", "diff", "bat", "echo", "printf", "test", "[", "source", ".")
	tunnels        = set("ngrok", "cloudflared", "localtunnel", "bore", "frpc", "serveo")
	firewalls      = set("ufw", "iptables", "ip6tables", "nft", "pfctl", "firewall-cmd", "ipfw")
	diskTools      = set("mkfs", "mke2fs", "newfs", "fdisk", "sfdisk", "parted", "wipefs", "mkswap")
	secretTools    = map[string]bool{"find-generic-password": true, "find-internet-password": true, "dump-keychain": true, "export": true}
)

func set(names ...string) map[string]bool {
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}
	return m
}

func commandName(w string) string { return strings.ToLower(filepath.Base(w)) }

var diskDevice = regexp.MustCompile(`^/dev/(sd|hd|vd|xvd|nvme|mmcblk|disk|rdisk)`)

func (g *guard) redirect(target string) {
	if diskDevice.MatchString(target) {
		g.add(reasonDisk)
	}
	if p, ok := g.expand(target); ok {
		if g.persistence(p) {
			g.add(reasonPersist)
		}
		if g.credential(p) {
			g.add(reasonCreds)
		}
	}
}

func (g *guard) segment(name string, args []string, seg segment, depth int) {
	g.paths(name, args)
	switch {
	case name == "cd" || name == "pushd":
		g.cwdMoved = true
	case name == "eval":
		g.command(strings.Join(args, " "), depth+1)
		g.fetchedAndRun(seg)
	case shells[name]:
		for i, a := range args {
			if strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.Contains(a, "c") && i+1 < len(args) {
				g.command(args[i+1], depth+1)
				g.fetchedAndRun(seg)
				break
			}
		}
		if seg.procSub {
			g.fetchedAndRun(seg)
		}
	case name == "rm":
		g.remove(args)
	case name == "find":
		g.find(args)
	case name == "chmod" || name == "chown" || name == "chgrp":
		g.recursive(args)
	case name == "dd":
		for _, a := range args {
			if v, ok := strings.CutPrefix(a, "of="); ok && diskDevice.MatchString(v) {
				g.add(reasonDisk)
			}
		}
	case name == "shred":
		for _, a := range args {
			if diskDevice.MatchString(a) {
				g.add(reasonDisk)
			}
		}
		g.recursive(args)
	case name == "mv":
		if len(args) >= 2 && args[len(args)-1] == "/dev/null" {
			g.broadTargets(args[:len(args)-1], false)
		}
	case name == "diskutil":
		if len(args) > 0 {
			sub := strings.ToLower(args[0])
			if strings.HasPrefix(sub, "erase") || strings.HasPrefix(sub, "partition") || strings.HasPrefix(sub, "zero") ||
				strings.HasPrefix(sub, "secureerase") || strings.HasPrefix(sub, "reformat") {
				g.add(reasonDisk)
			}
		}
	case isDiskTool(name):
		g.add(reasonDisk)
	case tunnels[name]:
		g.add(reasonPorts)
	case name == "lt":
		if hasWord(args, "--port") || hasWord(args, "-p") {
			g.add(reasonPorts)
		}
	case name == "tailscale":
		if hasWord(args, "funnel") {
			g.add(reasonPorts)
		}
	case name == "ssh":
		g.ssh(args)
	case name == "nc" || name == "ncat" || name == "netcat":
		for _, a := range args {
			if strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.Contains(a, "l") {
				g.add(reasonPorts)
			}
		}
	case name == "socat":
		for _, a := range args {
			if strings.Contains(strings.ToUpper(a), "LISTEN") {
				g.add(reasonPorts)
			}
		}
	case strings.HasPrefix(name, "python"):
		g.httpServer(args)
	case name == "docker" || name == "podman":
		g.container(args)
	case firewalls[name]:
		if !firewallListing(args) {
			g.add(reasonFirewall)
		}
	case name == "netsh":
		if hasWord(args, "advfirewall") || hasWord(args, "firewall") {
			g.add(reasonFirewall)
		}
	case name == "security":
		for _, a := range args {
			if secretTools[strings.ToLower(a)] {
				g.add(reasonCreds)
			}
		}
	case name == "crontab":
		if !(len(args) == 1 && args[0] == "-l") {
			g.add(reasonPersist)
		}
	case name == "visudo":
		g.add(reasonPersist)
	}
	if downloaders[name] || name == "scp" {
		g.upload(args)
	}
	for _, a := range args {
		if strings.Contains(a, "0.0.0.0") || strings.Contains(a, "[::]") {
			g.add(reasonPorts)
		}
	}
}

func isDiskTool(name string) bool {
	for t := range diskTools {
		if name == t || strings.HasPrefix(name, t+".") {
			return true
		}
	}
	return false
}

// fetchedAndRun flags a shell fed straight from a download inside the
// same command: sh -c "$(curl ...)", eval "$(wget ...)", bash <(curl ...).
func (g *guard) fetchedAndRun(seg segment) {
	for _, s := range seg.subs {
		for _, w := range strings.Fields(s) {
			if downloaders[commandName(w)] {
				g.add(reasonPipeSh)
			}
		}
	}
}

// paths flags credential and startup files a command names, unless it is
// only looking at them.
func (g *guard) paths(name string, args []string) {
	for _, a := range args {
		for _, w := range wordForms(a) {
			p, ok := g.expand(w)
			if !ok {
				continue
			}
			if g.credential(p) {
				g.add(reasonCreds)
			}
			if !viewers[name] && g.persistence(p) {
				g.add(reasonPersist)
			}
		}
	}
}

// wordForms are the paths a word may hold: itself, and what follows an
// "@" (curl -d @file) or "=" (--file=path, of=path).
func wordForms(w string) []string {
	forms := []string{w}
	if i := strings.IndexAny(w, "@="); i >= 0 && i+1 < len(w) {
		forms = append(forms, w[i+1:])
	}
	return forms
}

func (g *guard) upload(args []string) {
	for _, a := range args {
		if strings.HasPrefix(a, "@") {
			if p, ok := g.expand(a[1:]); ok && g.credential(p) {
				g.add(reasonCreds)
			}
		}
	}
}

func hasWord(args []string, w string) bool {
	for _, a := range args {
		if a == w {
			return true
		}
	}
	return false
}

func firewallListing(args []string) bool {
	for _, a := range args {
		switch a {
		case "-L", "-S", "-nvL", "--list", "--list-rules", "list", "status", "-s", "--state", "--version", "version", "show":
			return true
		}
	}
	return false
}

func (g *guard) ssh(args []string) {
	for i, a := range args {
		switch {
		case strings.HasPrefix(a, "-R"):
			g.add(reasonPorts)
		case a == "-g" || (strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && len(a) <= 6 && strings.Contains(a, "g") && !strings.ContainsAny(a, "0123456789:")):
			g.add(reasonPorts)
		case strings.HasPrefix(a, "-L") && i+1 <= len(args):
			spec := strings.TrimPrefix(a, "-L")
			if spec == "" && i+1 < len(args) {
				spec = args[i+1]
			}
			if strings.HasPrefix(spec, "0.0.0.0:") || strings.HasPrefix(spec, "*:") {
				g.add(reasonPorts)
			}
		}
	}
}

func (g *guard) httpServer(args []string) {
	module := false
	for i, a := range args {
		if a == "-m" && i+1 < len(args) && args[i+1] == "http.server" {
			module = true
		}
	}
	if !module {
		return
	}
	for i, a := range args {
		if (a == "--bind" || a == "-b") && i+1 < len(args) && isLoopback(args[i+1]) {
			return
		}
		if v, ok := strings.CutPrefix(a, "--bind="); ok && isLoopback(v) {
			return
		}
	}
	g.add(reasonPorts)
}

func isLoopback(h string) bool {
	h = strings.Trim(h, "[]")
	return h == "127.0.0.1" || h == "localhost" || h == "::1"
}

func (g *guard) container(args []string) {
	if len(args) == 0 || (args[0] != "run" && args[0] != "create") {
		return
	}
	for i, a := range args {
		spec := ""
		switch {
		case a == "-P" || a == "--publish-all":
			g.add(reasonPorts)
		case a == "--network=host" || a == "--net=host":
			g.add(reasonPorts)
		case (a == "--network" || a == "--net") && i+1 < len(args) && args[i+1] == "host":
			g.add(reasonPorts)
		case (a == "-p" || a == "--publish") && i+1 < len(args):
			spec = args[i+1]
		case strings.HasPrefix(a, "--publish="):
			spec = strings.TrimPrefix(a, "--publish=")
		case strings.HasPrefix(a, "-p") && len(a) > 2 && !strings.HasPrefix(a, "--"):
			spec = a[2:]
		}
		if spec != "" && !strings.HasPrefix(spec, "127.0.0.1:") && !strings.HasPrefix(spec, "localhost:") && !strings.HasPrefix(spec, "[::1]:") {
			g.add(reasonPorts)
		}
	}
}

// remove, find and recursive look for a wipe of something broad.
func (g *guard) remove(args []string) {
	recursive := false
	var targets []string
	endOpts := false
	for _, a := range args {
		switch {
		case !endOpts && a == "--":
			endOpts = true
		case !endOpts && a == "--recursive":
			recursive = true
		case !endOpts && strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--"):
			if strings.ContainsAny(a, "rR") {
				recursive = true
			}
		case !endOpts && strings.HasPrefix(a, "--"):
		default:
			targets = append(targets, a)
		}
	}
	if recursive {
		g.broadTargets(targets, false)
	}
}

func (g *guard) find(args []string) {
	var roots []string
	deletes := false
	for i, a := range args {
		if len(roots) == i && !strings.HasPrefix(a, "-") && a != "(" && a != "!" {
			roots = append(roots, a)
		}
		if a == "-delete" {
			deletes = true
		}
		if (a == "-exec" || a == "-execdir") && i+1 < len(args) && (commandName(args[i+1]) == "rm" || commandName(args[i+1]) == "shred") {
			deletes = true
		}
	}
	if deletes {
		g.broadTargets(roots, true)
	}
}

func (g *guard) recursive(args []string) {
	rec := false
	var targets []string
	for _, a := range args {
		switch {
		case a == "--recursive" || (strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.ContainsAny(a, "rR")):
			rec = true
		case strings.HasPrefix(a, "-"):
		default:
			targets = append(targets, a)
		}
	}
	if rec {
		g.broadTargets(targets, true)
	}
}

func (g *guard) broadTargets(targets []string, allowWorkspace bool) {
	for _, t := range targets {
		if p, ok := g.expand(t); ok && g.broad(p, allowWorkspace) {
			g.add(reasonWipe)
			return
		}
	}
}

var systemRoots = []string{
	"/usr", "/etc", "/var", "/bin", "/sbin", "/lib", "/lib32", "/lib64", "/opt", "/boot", "/dev", "/proc", "/sys",
	"/root", "/home", "/Users", "/Volumes", "/System", "/Library", "/Applications", "/private", "/srv", "/mnt", "/media",
}

// broad reports whether deleting p would take out the filesystem root,
// the home folder or a parent of it, a system folder, a top-level folder
// of the home folder, or the workspace (or a parent of it).
func (g *guard) broad(p string, allowWorkspace bool) bool {
	switch {
	case p == "/":
		return true
	case g.home != "" && within(g.home, p):
		return true
	case g.ws != "" && within(g.ws, p) && (p != g.ws || !allowWorkspace):
		return true
	}
	for _, root := range systemRoots {
		if within(p, root) && depthBelow(p, root) <= 1 {
			return true
		}
	}
	if g.home != "" && within(p, g.home) && depthBelow(p, g.home) == 1 && !(g.ws != "" && within(p, g.ws)) {
		return true
	}
	return false
}

// expand resolves a command word to an absolute path, or reports that it
// cannot (an unknown variable, a pattern, a relative path after a cd).
func (g *guard) expand(w string) (string, bool) {
	w = strings.TrimSpace(w)
	if w == "" {
		return "", false
	}
	for _, v := range []string{"${HOME}", "$HOME"} {
		if rest, ok := strings.CutPrefix(w, v); ok && g.home != "" && (rest == "" || rest[0] == '/') {
			w = g.home + rest
		}
	}
	for _, v := range []string{"${PWD}", "$PWD"} {
		if rest, ok := strings.CutPrefix(w, v); ok && g.ws != "" && !g.cwdMoved && (rest == "" || rest[0] == '/') {
			w = g.ws + rest
		}
	}
	if w == "~" || strings.HasPrefix(w, "~/") {
		if g.home == "" {
			return "", false
		}
		w = g.home + w[1:]
	}
	if strings.Contains(w, "$") {
		return "", false
	}
	if i := strings.IndexAny(w, "*?["); i >= 0 {
		// Only a pattern that is the whole last component ("dir/*") stands for
		// the folder's contents; anything else names files by pattern.
		last := w[strings.LastIndex(w[:i], "/")+1:]
		if last != "*" && last != ".*" || strings.ContainsAny(w[:i], "?[") {
			return "", false
		}
		w = w[:len(w)-len(last)]
		if w == "" {
			w = "."
		}
	}
	if !filepath.IsAbs(w) {
		if g.ws == "" || g.cwdMoved {
			return "", false
		}
		w = filepath.Join(g.ws, w)
	}
	return filepath.Clean(w), true
}

// within reports whether p is root or lies under it.
func within(p, root string) bool {
	if caseBlindFS {
		p, root = strings.ToLower(p), strings.ToLower(root)
	}
	if root == "/" {
		return strings.HasPrefix(p, "/")
	}
	return p == root || strings.HasPrefix(p, root+"/")
}

func depthBelow(p, root string) int {
	rel := strings.Trim(strings.TrimPrefix(filepath.ToSlash(p), filepath.ToSlash(root)), "/")
	if rel == "" {
		return 0
	}
	return strings.Count(rel, "/") + 1
}

var (
	credentialHome = []string{
		".ssh", ".aws", ".gnupg", ".gpg", ".kube", ".azure", ".oci", ".config/gcloud", ".config/gh",
		".docker/config.json", ".netrc", ".git-credentials", ".pgpass", ".npmrc", ".pypirc", "Library/Keychains",
	}
	credentialAbs = []string{"/etc/shadow", "/etc/master.passwd", "/etc/sudoers", "/etc/sudoers.d"}
	otherHomes    = regexp.MustCompile(`^/(Users|home)/[^/]+/(\.(ssh|aws|gnupg|kube|azure|oci)|\.config/(gcloud|gh)|Library/Keychains)(/|$)|^/root/\.(ssh|aws|gnupg|kube)(/|$)`)
	persistHome   = []string{
		".bashrc", ".bash_profile", ".bash_login", ".profile", ".zshrc", ".zprofile", ".zshenv", ".zlogin",
		".config/fish", ".config/systemd/user", "Library/LaunchAgents", "Library/LaunchDaemons",
	}
	persistAbs = []string{
		"/etc/profile", "/etc/profile.d", "/etc/bashrc", "/etc/zshrc", "/etc/cron.d", "/etc/cron.daily", "/etc/crontab",
		"/etc/systemd/system", "/etc/sudoers", "/etc/sudoers.d", "/etc/ld.so.preload", "/Library/LaunchAgents", "/Library/LaunchDaemons",
	}
)

func (g *guard) credential(p string) bool {
	for _, a := range credentialAbs {
		if within(p, a) {
			return true
		}
	}
	if g.home != "" {
		for _, h := range credentialHome {
			if within(p, g.home+"/"+h) {
				return true
			}
		}
	}
	return otherHomes.MatchString(p)
}

func (g *guard) persistence(p string) bool {
	for _, a := range persistAbs {
		if within(p, a) {
			return true
		}
	}
	if g.home != "" {
		for _, h := range persistHome {
			if within(p, g.home+"/"+h) {
				return true
			}
		}
	}
	return false
}
