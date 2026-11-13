package runtime

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Ady-JK/paladin/core/internal/media"
)

// Base64 plus JSON must stay below both the host scanner and desktop
// sidecar's 16 MiB per-request buffer.
const maxAttachmentBytes = 10 << 20

var attachmentID = regexp.MustCompile(`^file-[a-zA-Z0-9]+$`)

// SaveTaggedImage copies a user-selected image into the session, including
// screenshots selected outside the workspace. Non-images remain path tags.
func (r *AgentRuntime) SaveTaggedImage(sessionID, cwd, path string) (*media.Attachment, error) {
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("tagged image path must be absolute")
	}
	image, ok, err := media.ReadIfImage(path)
	if err != nil || !ok {
		return nil, err
	}
	dataURL, err := image.DataURL()
	if err != nil {
		return nil, err
	}
	_, data, ok := strings.Cut(dataURL, ",")
	if !ok {
		return nil, fmt.Errorf("invalid image data")
	}
	return r.SaveAttachment(sessionID, cwd, filepath.Base(path), data)
}

func (r *AgentRuntime) SaveAttachment(sessionID, cwd, name, encoded string) (*media.Attachment, error) {
	if strings.TrimSpace(sessionID) == "" || strings.TrimSpace(name) == "" || len(name) > 255 || strings.ContainsAny(name, "\r\n\x00") || len(encoded) == 0 || len(encoded) > base64.StdEncoding.EncodedLen(maxAttachmentBytes) {
		return nil, fmt.Errorf("invalid attachment")
	}
	sess, err := r.transcripts.Load(cwd, sessionID)
	if err != nil {
		return nil, err
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(data) == 0 || len(data) > maxAttachmentBytes {
		return nil, fmt.Errorf("invalid attachment bytes")
	}
	dir := filepath.Join(sess.Dir, "attachments")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	f, err := os.CreateTemp(dir, "file-*")
	if err != nil {
		return nil, err
	}
	path := f.Name()
	defer func() { _ = f.Close() }()
	if err := f.Chmod(0600); err != nil {
		_ = os.Remove(path)
		return nil, err
	}
	if _, err := f.Write(data); err != nil {
		_ = os.Remove(path)
		return nil, err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return nil, err
	}
	ref := &media.Attachment{ID: filepath.Base(path), Name: filepath.Base(name), Path: path, Size: int64(len(data)), SHA256: fmt.Sprintf("%x", sha256.Sum256(data)), MIMEType: http.DetectContentType(data)}
	if mime := media.MIME(data); mime != "" {
		image, err := media.Read(path)
		if err != nil {
			_ = os.Remove(path)
			return nil, err
		}
		ref.Image, ref.MIMEType = image, mime
	}
	metadata, err := json.Marshal(ref)
	if err != nil {
		_ = os.Remove(path)
		return nil, err
	}
	if err := os.WriteFile(path+".json", metadata, 0600); err != nil {
		_ = os.Remove(path)
		return nil, err
	}
	return ref, nil
}

func (r *AgentRuntime) SessionAttachment(sessionID, cwd, id string) (*media.Attachment, error) {
	if !attachmentID.MatchString(id) {
		return nil, fmt.Errorf("invalid attachment id")
	}
	sess, err := r.transcripts.Load(cwd, sessionID)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(sess.Dir, "attachments", id)
	data, err := os.ReadFile(path + ".json")
	if err != nil {
		return nil, err
	}
	var ref media.Attachment
	if err := json.Unmarshal(data, &ref); err != nil {
		return nil, err
	}
	if ref.ID != id || ref.Path != path {
		return nil, fmt.Errorf("invalid attachment metadata")
	}
	st, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() || st.Size() != ref.Size {
		return nil, fmt.Errorf("attachment changed")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	hash := sha256.New()
	_, copyErr := io.Copy(hash, io.LimitReader(f, maxAttachmentBytes+1))
	closeErr := f.Close()
	if copyErr != nil {
		return nil, copyErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if fmt.Sprintf("%x", hash.Sum(nil)) != ref.SHA256 {
		return nil, fmt.Errorf("attachment changed")
	}
	if ref.Image != nil {
		if _, err := ref.Image.DataURL(); err != nil {
			return nil, err
		}
	}
	return &ref, nil
}

func (r *AgentRuntime) SessionImage(sessionID, cwd, path string) (string, error) {
	sess, err := r.transcripts.Load(cwd, sessionID)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(sess.Dir, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("image is outside session")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	realSession, err := filepath.EvalSymlinks(sess.Dir)
	if err != nil {
		return "", err
	}
	realRel, err := filepath.Rel(realSession, resolved)
	if err != nil || realRel == ".." || strings.HasPrefix(realRel, ".."+string(filepath.Separator)) || filepath.IsAbs(realRel) {
		return "", fmt.Errorf("image is outside session")
	}
	// Only paths already recorded in this session's transcript are readable.
	msgs, err := sess.History()
	if err != nil {
		return "", err
	}
	for _, msg := range msgs {
		if msg.Image != nil && msg.Image.Path == path {
			return msg.Image.DataURL()
		}
		for _, attachment := range msg.Attachments {
			if attachment.Image != nil && attachment.Image.Path == path {
				return attachment.Image.DataURL()
			}
		}
	}
	return "", fmt.Errorf("image not found in session")
}
