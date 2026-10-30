package runtime

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type sideChatServer struct {
	mu     sync.Mutex
	bodies []string
	answer func(n int) (string, int)
	gate   chan struct{}
}

func (s *sideChatServer) start(t *testing.T) *AgentRuntime {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		s.bodies = append(s.bodies, string(body))
		n := len(s.bodies)
		s.mu.Unlock()
		if s.gate != nil {
			<-s.gate
		}
		text, status := "answer", 200
		if s.answer != nil {
			text, status = s.answer(n)
		}
		if status != 200 {
			http.Error(w, `{"error":{"message":"bad"}}`, status)
			return
		}
		reply(w, body, text, 0)
	}))
	t.Cleanup(srv.Close)
	rt, _ := testRuntime(t)
	if err := rt.store.Keys.Set("openrouter", "sk-test"); err != nil {
		t.Fatal(err)
	}
	pointMockAt(rt, srv.URL)
	return rt
}

func (s *sideChatServer) last() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.bodies[len(s.bodies)-1]
}

func TestSideQuestionStartsAPersistedChatAndContinuesIt(t *testing.T) {
	s := &sideChatServer{answer: func(n int) (string, int) {
		if n == 1 {
			return "first side answer", 200
		}
		return "second side answer", 200
	}}
	rt := s.start(t)
	ws := t.TempDir()
	sess, err := rt.OpenSession("", ws)
	if err != nil {
		t.Fatal(err)
	}
	first, err := rt.SideQuestion(context.Background(), SideQuestionRequest{SessionID: sess.SessionID, Cwd: ws, Question: "what is X?"})
	if err != nil {
		t.Fatal(err)
	}
	if first.ChatID == "" {
		t.Fatal("a new side question must start a chat")
	}
	second, err := rt.SideQuestion(context.Background(), SideQuestionRequest{SessionID: sess.SessionID, Cwd: ws, ChatID: first.ChatID, Question: "and Y?"})
	if err != nil {
		t.Fatal(err)
	}
	if second.ChatID != first.ChatID || second.Answer != "second side answer" {
		t.Fatalf("follow-up = %+v", second)
	}
	body := s.last()
	for _, want := range []string{"what is X?", "first side answer", "and Y?"} {
		if !strings.Contains(body, want) {
			t.Fatalf("follow-up request lacks %q: %s", want, body)
		}
	}
	list, err := rt.ListSideChats(sess.SessionID, ws)
	if err != nil || len(list) != 1 || list[0].MessageCount != 4 || list[0].Title != "what is X?" {
		t.Fatalf("list = %+v, %v", list, err)
	}
	chat, err := rt.LoadSideChat(sess.SessionID, ws, first.ChatID)
	if err != nil || chat.Messages[3].Content != "second side answer" || chat.Messages[3].Model == "" {
		t.Fatalf("chat = %+v, %v", chat, err)
	}
	loaded, _ := rt.transcripts.Load(ws, sess.SessionID)
	if h, _ := loaded.History(); len(h) != 0 {
		t.Fatalf("side chat leaked into the transcript: %d messages", len(h))
	}
	if err := rt.DeleteSideChat(sess.SessionID, ws, first.ChatID); err != nil {
		t.Fatal(err)
	}
	if list, _ := rt.ListSideChats(sess.SessionID, ws); len(list) != 0 {
		t.Fatalf("deleted chat still listed: %+v", list)
	}
}

func TestSideQuestionFailureSavesNothing(t *testing.T) {
	s := &sideChatServer{answer: func(int) (string, int) { return "", 400 }}
	rt := s.start(t)
	ws := t.TempDir()
	sess, _ := rt.OpenSession("", ws)
	if _, err := rt.SideQuestion(context.Background(), SideQuestionRequest{SessionID: sess.SessionID, Cwd: ws, Question: "q"}); err == nil {
		t.Fatal("expected the model error")
	}
	if list, _ := rt.ListSideChats(sess.SessionID, ws); len(list) != 0 {
		t.Fatalf("a failed first question must not leave a chat: %+v", list)
	}
}

func TestSideQuestionUnknownChatFails(t *testing.T) {
	rt := (&sideChatServer{}).start(t)
	ws := t.TempDir()
	sess, _ := rt.OpenSession("", ws)
	if _, err := rt.SideQuestion(context.Background(), SideQuestionRequest{SessionID: sess.SessionID, Cwd: ws, ChatID: "8a1f7c4e-0000-4000-8000-000000000000", Question: "q"}); err == nil {
		t.Fatal("unknown chat must fail")
	}
}

func TestSideChatRefusesASecondQuestionWhileOneIsPending(t *testing.T) {
	s := &sideChatServer{}
	rt := s.start(t)
	ws := t.TempDir()
	sess, _ := rt.OpenSession("", ws)
	first, err := rt.SideQuestion(context.Background(), SideQuestionRequest{SessionID: sess.SessionID, Cwd: ws, Question: "q1"})
	if err != nil {
		t.Fatal(err)
	}
	s.gate = make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := rt.SideQuestion(context.Background(), SideQuestionRequest{SessionID: sess.SessionID, Cwd: ws, ChatID: first.ChatID, Question: "q2"})
		done <- err
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		s.mu.Lock()
		n := len(s.bodies)
		s.mu.Unlock()
		if n == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("second question never reached the model")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, err := rt.SideQuestion(context.Background(), SideQuestionRequest{SessionID: sess.SessionID, Cwd: ws, ChatID: first.ChatID, Question: "q3"}); err == nil || !strings.Contains(err.Error(), "busy") {
		t.Fatalf("concurrent question = %v, want busy", err)
	}
	close(s.gate)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if chat, _ := rt.LoadSideChat(sess.SessionID, ws, first.ChatID); len(chat.Messages) != 4 {
		t.Fatalf("chat has %d messages, want 4", len(chat.Messages))
	}
}
