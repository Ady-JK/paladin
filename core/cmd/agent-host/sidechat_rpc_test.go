package main

import (
	"testing"

	"github.com/Ady-JK/paladin/core/internal/transcript"
)

func TestSideChatMethods(t *testing.T) {
	rt := testRuntime(t)
	mustOK(t, callUnary(rt, "0", "addProvider", map[string]string{"name": "mock", "apiUrl": "http://127.0.0.1:1", "apiKey": "sk-live", "apiType": "openai"}))
	ws := t.TempDir()
	sess, err := rt.OpenSession("", ws)
	if err != nil {
		t.Fatal(err)
	}
	list := decodeAs[[]transcript.SideChatInfo](t, callUnary(rt, "1", "listSideChats", map[string]any{"sessionId": sess.SessionID, "cwd": ws}))
	if len(list) != 0 {
		t.Fatalf("%+v", list)
	}
	mustFail(t, callUnary(rt, "2", "loadSideChat", map[string]any{"sessionId": sess.SessionID, "cwd": ws, "chatId": "../x"}), "not found")
	mustFail(t, callUnary(rt, "3", "deleteSideChat", map[string]any{"sessionId": sess.SessionID, "cwd": ws, "chatId": "nope"}), "not found")
}
