// sidechat.go stores /btw side chats: short multi-turn exchanges about
// a session that never enter its transcript. Each chat lives at
// <session dir>/side-chats/<uuid>/chat.json.

package transcript

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
	"uuid"

	"github.com/Ady-JK/paladin/core/internal/jsonx"
	"github.com/Ady-JK/paladin/core/internal/syserr"
)

const sideChatsDir = "side-chats"

type SideChatMessage struct {
	Role      Role      `json:"role"`
	Content   string    `json:"content"`
	Model     string    `json:"model,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
}

type SideChat struct {
	ID        string            `json:"id"`
	Title     string            `json:"title"`
	CreatedAt time.Time         `json:"createdAt"`
	UpdatedAt time.Time         `json:"updatedAt"`
	Messages  []SideChatMessage `json:"messages"`
}

// SideChatInfo is a side chat without its messages, for listing.
type SideChatInfo struct {
	ID           string    `json:"id"`
	Title        string    `json:"title"`
	UpdatedAt    time.Time `json:"updatedAt"`
	MessageCount int       `json:"messageCount"`
}

// NewSideChat starts an empty chat titled after its first question.
func NewSideChat(question string) SideChat {
	now := time.Now().UTC()
	return SideChat{ID: uuid.New().String(), Title: shorten(question, 60), CreatedAt: now, UpdatedAt: now}
}

// Add appends one message and bumps UpdatedAt.
func (c *SideChat) Add(role Role, content, model string) {
	now := time.Now().UTC()
	c.Messages = append(c.Messages, SideChatMessage{Role: role, Content: content, Model: model, CreatedAt: now})
	c.UpdatedAt = now
}

func sideChatDir(sessionDir, id string) (string, error) {
	if _, err := uuid.Parse(id); err != nil {
		return "", fmt.Errorf("side chat %q not found", id)
	}
	return filepath.Join(sessionDir, sideChatsDir, id), nil
}

func SaveSideChat(sessionDir string, c SideChat) error {
	dir, err := sideChatDir(sessionDir, c.ID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("io: side chat: %s", syserr.Str(err))
	}
	data, err := jsonx.Marshal(c)
	if err != nil {
		return fmt.Errorf("io: encode side chat: %s", syserr.Str(err))
	}
	return atomicWrite(dir, "chat.json", data)
}

func LoadSideChat(sessionDir, id string) (SideChat, error) {
	dir, err := sideChatDir(sessionDir, id)
	if err != nil {
		return SideChat{}, err
	}
	data, err := os.ReadFile(filepath.Join(dir, "chat.json"))
	if errors.Is(err, os.ErrNotExist) {
		return SideChat{}, fmt.Errorf("side chat %q not found", id)
	}
	if err != nil {
		return SideChat{}, fmt.Errorf("io: side chat: %s", syserr.Str(err))
	}
	var c SideChat
	if err := json.Unmarshal(data, &c); err != nil {
		return SideChat{}, fmt.Errorf("io: side chat %s is corrupt: %s", id, syserr.Str(err))
	}
	return c, nil
}

// ListSideChats returns the session's readable side chats, newest
// first. Unreadable entries are skipped so one bad file hides nothing else.
func ListSideChats(sessionDir string) ([]SideChatInfo, error) {
	entries, err := os.ReadDir(filepath.Join(sessionDir, sideChatsDir))
	if errors.Is(err, os.ErrNotExist) {
		return []SideChatInfo{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("io: side chats: %s", syserr.Str(err))
	}
	list := []SideChatInfo{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		c, err := LoadSideChat(sessionDir, e.Name())
		if err != nil {
			continue
		}
		list = append(list, SideChatInfo{ID: c.ID, Title: c.Title, UpdatedAt: c.UpdatedAt, MessageCount: len(c.Messages)})
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].UpdatedAt.After(list[j].UpdatedAt) })
	return list, nil
}

func DeleteSideChat(sessionDir, id string) error {
	dir, err := sideChatDir(sessionDir, id)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("io: delete side chat: %s", syserr.Str(err))
	}
	return nil
}
