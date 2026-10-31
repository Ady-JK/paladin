// agent-host is the stdio sidecar the Electron main process spawns.
// Protocol is JSONL: one JSON object per line.
//
// Electron -> Go (stdin):
//
//	{"id":1,"method":"listProviders","params":{}}
//	{"id":2,"method":"submitChat","params":{"sessionId":"...","message":"hi","cwd":"/tmp/proj"}}
//
// Go -> Electron (stdout):
//
//	response: {"id":1,"ok":true,"result":[...]} | {"id":1,"ok":false,"error":"..."}
//	event:    {"event":"chat/delta","seq":7,"sessionId":"...","turnId":"...","delta":"..."}
//	          {"event":"turn/done","seq":9,"sessionId":"...","turnId":"...","turn":{...}}
//
// Control calls answer inline; other requests run on a bounded pool,
// in order per session (see dispatcher.go). Turns run in the background and
// stream events (see desktop-app/PROTOCOL.md). Logs go to stderr so
// stdout stays pure JSONL.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Ady-JK/paladin/core/internal/config"
	"github.com/Ady-JK/paladin/core/internal/runtime"
	"github.com/Ady-JK/paladin/core/internal/sandbox"
)

func main() {
	// The Linux sandbox re-execs agent-host to confine a shell command.
	if len(os.Args) > 1 && os.Args[1] == sandbox.HelperArg {
		os.Exit(sandbox.RunHelper(os.Args[2:]))
	}
	dataDir := config.DataDir()
	initLogging(dataDir)
	rt := runtime.New(dataDir, config.NewConfig(dataDir))
	defer userTerminals.closeAll()
	installTurnSink(rt)
	installMCPSink(rt)
	installToolsSink(rt)
	installAgentsSink(rt)
	installBrowserSink(rt)
	rt.StartMCP()
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGINT)
	go handleSignals(rt, sigs, os.Exit)

	fmt.Fprintf(os.Stderr, "agent-host ready dir=%s\n", rt.GetWorkspace())

	requests := newDispatcher(rt, emitResponse, defaultLimits)
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var req Request
		if err := json.Unmarshal(line, &req); err != nil {
			logf("WARN bad request line: %v", err)
			continue
		}
		requests.dispatch(&req)
	}
	if err := scanner.Err(); err != nil {
		logf("WARN read stdin: %v", err)
	}
	// The client is gone: stop every turn and let them persist.
	rt.ShutdownTurns(20 * time.Second)
	requests.close(10 * time.Second)
	notices.flush()
}

// handleSignals treats SIGTERM/SIGINT like the client hanging up: stop
// every turn so it persists what it did, then exit cleanly.
func handleSignals(rt *runtime.AgentRuntime, sigs <-chan os.Signal, exit func(int)) {
	sig, ok := <-sigs
	if !ok {
		return
	}
	logf("agent-host: %v, stopping turns", sig)
	userTerminals.closeAll()
	rt.ShutdownTurns(10 * time.Second)
	exit(0)
}
