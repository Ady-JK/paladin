package runtime

import (
	"context"
	"testing"

	"github.com/Ady-JK/paladin/core/internal/config"
	"github.com/Ady-JK/paladin/core/internal/transcript"
)

func TestContextThresholdPerModel(t *testing.T) {
	rt, _ := testRuntime(t)
	view, err := rt.CompactionGet("litellm", "gpt-5.6-luna")
	if err != nil || view.Percent != 90 || view.Window <= 0 {
		t.Fatalf("default: %+v %v", view, err)
	}
	view, err = rt.CompactionSet("litellm", "gpt-5.6-luna", 80.5)
	if err != nil || view.Percent != 80.5 {
		t.Fatalf("set: %+v %v", view, err)
	}
	other, err := rt.CompactionGet("litellm", "other-model")
	if err != nil || other.Percent != 90 {
		t.Fatalf("other: %+v %v", other, err)
	}
	if full, err := rt.CompactionSet("litellm", "gpt-5.6-luna", 100); err != nil || full.Percent != 100 {
		t.Fatalf("full limit: %+v %v", full, err)
	}
	if _, err = rt.CompactionSet("litellm", "gpt-5.6-luna", 101); err == nil {
		t.Fatal("accepted unsafe threshold")
	}
}

func TestSelectedContextWindow(t *testing.T) {
	meta := config.ModelMeta{ContextWindow: 1000000, MaxInput: 936000, DefaultInput: 200000}
	if got := selectedContextWindow(meta, "sonnet", 0); got != 936000 {
		t.Fatalf("free long context: %d", got)
	}
	if got := selectedContextWindow(meta, "sonnet", 200000); got != 200000 {
		t.Fatalf("selected default: %d", got)
	}
	meta.LongContextSurcharge = true
	if got := selectedContextWindow(meta, "luna", 0); got != 200000 {
		t.Fatalf("priced default: %d", got)
	}
	if got := selectedContextWindow(meta, "luna", 936000); got != 936000 {
		t.Fatalf("selected long: %d", got)
	}
}

func TestContextChoicePersistsAndRejectsUnadvertisedSize(t *testing.T) {
	rt, _ := testRuntime(t)
	meta := config.ModelMeta{ContextWindow: 1000000, MaxInput: 936000, DefaultInput: 200000}
	rt.SetCatalogFetcher(func(context.Context, config.Provider, string) (CatalogFetch, error) {
		return CatalogFetch{Models: []string{"sonnet"}, Meta: map[string]config.ModelMeta{"sonnet": meta}}, nil
	})
	if _, err := rt.RefreshProvider("litellm"); err != nil {
		t.Fatal(err)
	}
	view, err := rt.CompactionGet("litellm", "sonnet")
	if err != nil || view.Window != 936000 || len(view.Options) != 2 {
		t.Fatalf("initial: %+v %v", view, err)
	}
	short := int64(200000)
	if _, err := rt.CompactionSetWindow("litellm", "sonnet", 85, &short); err != nil {
		t.Fatal(err)
	}
	view, err = rt.CompactionGet("litellm", "sonnet")
	if err != nil || view.Window != 200000 || view.Percent != 85 {
		t.Fatalf("saved: %+v %v", view, err)
	}
	unsupported := int64(1000000)
	if _, err := rt.CompactionSetWindow("litellm", "sonnet", 90, &unsupported); err == nil {
		t.Fatal("accepted unadvertised input limit")
	}
}

func TestContextChoicesPersistPerSession(t *testing.T) {
	rt, _ := testRuntime(t)
	meta := config.ModelMeta{ContextWindow: 1000000, MaxInput: 936000, DefaultInput: 200000}
	rt.SetCatalogFetcher(func(context.Context, config.Provider, string) (CatalogFetch, error) {
		return CatalogFetch{Models: []string{"sonnet"}, Meta: map[string]config.ModelMeta{"sonnet": meta}}, nil
	})
	if _, err := rt.RefreshProvider("litellm"); err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	a, err := rt.transcripts.Create(cwd, "litellm", "other-model")
	if err != nil {
		t.Fatal(err)
	}
	b, err := rt.transcripts.Create(cwd, "litellm", "sonnet")
	if err != nil {
		t.Fatal(err)
	}
	short := int64(200000)
	if _, err := rt.CompactionSetForSession("litellm", "sonnet", 75, &short, a.Meta.SessionID, cwd); err != nil {
		t.Fatal(err)
	}
	first, err := rt.CompactionGetForSession("litellm", "sonnet", a.Meta.SessionID, cwd)
	if err != nil || first.Window != 200000 || first.Percent != 75 {
		t.Fatalf("first: %+v %v", first, err)
	}
	second, err := rt.CompactionGetForSession("litellm", "sonnet", b.Meta.SessionID, cwd)
	if err != nil || second.Window != 936000 || second.Percent != 90 {
		t.Fatalf("second: %+v %v", second, err)
	}
	reopened, err := rt.transcripts.Load(cwd, a.Meta.SessionID)
	if err != nil || reopened.Meta.SessionID != a.Meta.SessionID {
		t.Fatal(err)
	}
	if reopened.Meta.Model == nil || *reopened.Meta.Model != "sonnet" {
		t.Fatalf("session model not saved: %+v", reopened.Meta.Model)
	}
	first, err = rt.CompactionGetForSession("litellm", "sonnet", a.Meta.SessionID, cwd)
	if err != nil || first.Window != 200000 || first.Percent != 75 {
		t.Fatalf("reopened: %+v %v", first, err)
	}
}

func TestSessionContextFreezesDefaultsAndKeepsConcurrentEdits(t *testing.T) {
	rt, _ := testRuntime(t)
	cwd := t.TempDir()
	sess, err := rt.transcripts.Create(cwd, "litellm", "sonnet")
	if err != nil {
		t.Fatal(err)
	}
	choice, err := rt.sessionContextChoice(cwd, sess.Meta.SessionID, sess.Meta, "litellm", "sonnet", 936000, 0)
	if err != nil || choice.Window != 936000 || choice.Percent != 90 {
		t.Fatalf("default choice %+v %v", choice, err)
	}
	newer := int64(200000)
	rt.SetCatalogFetcher(func(context.Context, config.Provider, string) (CatalogFetch, error) {
		return CatalogFetch{Models: []string{"sonnet"}, Meta: map[string]config.ModelMeta{"sonnet": {ContextWindow: 1000000, MaxInput: 936000, DefaultInput: 200000}}}, nil
	})
	if _, err = rt.RefreshProvider("litellm"); err != nil {
		t.Fatal(err)
	}
	if _, err = rt.CompactionSetForSession("litellm", "sonnet", 75, &newer, sess.Meta.SessionID, cwd); err != nil {
		t.Fatal(err)
	}
	choice, err = rt.sessionContextChoice(cwd, sess.Meta.SessionID, sess.Meta, "litellm", "sonnet", 936000, 90)
	if err != nil || choice.Window != 200000 || choice.Percent != 75 {
		t.Fatalf("concurrent edit %+v %v", choice, err)
	}
}

func TestForkKeepsSessionContextChoices(t *testing.T) {
	rt, _ := testRuntime(t)
	cwd := t.TempDir()
	sess, err := rt.transcripts.Create(cwd, "litellm", "sonnet")
	if err != nil {
		t.Fatal(err)
	}
	err = rt.transcripts.UpdateMeta(cwd, sess.Meta.SessionID, func(m *transcript.Meta) error {
		m.ContextChoices = map[string]map[string]transcript.ContextChoice{"litellm": {"sonnet": {Window: 200000, Percent: 75}}}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	fork, err := rt.transcripts.Fork(cwd, sess.Meta.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := rt.transcripts.Load(cwd, fork.Meta.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if got := reopened.Meta.ContextChoices["litellm"]["sonnet"]; got.Window != 200000 || got.Percent != 75 {
		t.Fatalf("fork choice: %+v", got)
	}
}
