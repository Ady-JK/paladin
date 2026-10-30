package transcript

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCreateChildLinksBothSidesAndSitsBesideTheParent(t *testing.T) {
	store := NewStore(t.TempDir())
	parent, err := store.Create("/w", "p", "m")
	if err != nil {
		t.Fatal(err)
	}
	_ = parent.Append(RoleUser, "parent question")
	link := Subagent{ParentID: parent.Meta.SessionID, ParentTurnID: "t1", SpawnCallID: "c1", Type: "explore", Name: "scout", ForkTurns: "none", Status: "queued"}
	child, err := store.CreateChild(parent.Meta.SessionID, "/w", link, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if child.Meta.Subagent == nil || child.Meta.Subagent.ParentID != parent.Meta.SessionID || child.Meta.Subagent.Type != "explore" {
		t.Fatalf("child link = %+v", child.Meta.Subagent)
	}
	if child.Meta.ForkedFrom != "" {
		t.Fatal("a subagent is not a user fork")
	}
	if filepath.Dir(child.Dir) != filepath.Dir(parent.Dir) {
		t.Fatalf("child %s is not a sibling of %s", child.Dir, parent.Dir)
	}
	if child.Meta.Title != "scout" || *child.Meta.Model != "m" {
		t.Fatalf("child meta = %+v", child.Meta)
	}
	if msgs, _ := child.History(); len(msgs) != 0 {
		t.Fatalf("fresh child has history %+v", msgs)
	}
	reloaded, err := store.Load("/w", parent.Meta.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.Meta.Children) != 1 || reloaded.Meta.Children[0] != child.Meta.SessionID {
		t.Fatalf("parent children = %v", reloaded.Meta.Children)
	}
	// Reloading from disk (an app restart) keeps the relationship.
	again, err := NewStore(store.Root).Load("/w", child.Meta.SessionID)
	if err != nil || again.Meta.Subagent == nil || again.Meta.Subagent.ParentID != parent.Meta.SessionID {
		t.Fatalf("reload child = %+v, %v", again.Meta, err)
	}
}

func TestCreateChildCopiesTheGivenSnapshotAndOverridesModel(t *testing.T) {
	store := NewStore(t.TempDir())
	parent, _ := store.Create("/w", "p", "m")
	snap := []Message{UserMessage("one"), {Role: RoleAssistant, Content: "two"}}
	child, err := store.CreateChild(parent.Meta.SessionID, "/w", Subagent{ParentID: parent.Meta.SessionID, Type: "reviewer", Name: "r", ForkTurns: "all"}, snap, "", "other-model")
	if err != nil {
		t.Fatal(err)
	}
	msgs, _ := child.History()
	if len(msgs) != 2 || msgs[1].Content != "two" {
		t.Fatalf("snapshot = %+v", msgs)
	}
	if *child.Meta.Model != "other-model" {
		t.Fatalf("model = %v", *child.Meta.Model)
	}
}

func TestCreateChildNeedsAnExistingParent(t *testing.T) {
	store := NewStore(t.TempDir())
	if _, err := store.CreateChild("00000000-0000-4000-8000-000000000999", "/w", Subagent{Type: "explore"}, nil, "", ""); err == nil {
		t.Fatal("missing parent accepted")
	}
	entries, _ := os.ReadDir(store.Root)
	for _, ns := range entries {
		inner, _ := os.ReadDir(filepath.Join(store.Root, ns.Name()))
		if len(inner) != 0 {
			t.Fatalf("left a child folder behind: %v", inner)
		}
	}
}

func TestUpdateSubagentPersistsStatus(t *testing.T) {
	store := NewStore(t.TempDir())
	parent, _ := store.Create("/w", "p", "m")
	child, _ := store.CreateChild(parent.Meta.SessionID, "/w", Subagent{ParentID: parent.Meta.SessionID, Type: "explore", Name: "x", Status: "queued"}, nil, "", "")
	err := store.UpdateMeta("/w", child.Meta.SessionID, func(m *Meta) error {
		m.Subagent.Status, m.Subagent.Result = "completed", "found it"
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	again, _ := store.Load("/w", child.Meta.SessionID)
	if again.Meta.Subagent.Status != "completed" || again.Meta.Subagent.Result != "found it" {
		t.Fatalf("status = %+v", again.Meta.Subagent)
	}
}
