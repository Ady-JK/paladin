// Package media keeps bounded, content-checked image references for model input.
package media

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

const MaxBytes = 4 << 20

// Image is a reference to bytes on disk, not a base64 copy in the transcript.
// The hash makes a changed file unavailable instead of silently showing the
// model an image different from the one the agent originally read.
type Image struct {
	Path     string `json:"path"`
	MIMEType string `json:"mimeType"`
	Size     int64  `json:"size"`
	SHA256   string `json:"sha256"`
}

func MIME(header []byte) string {
	switch {
	case len(header) >= 8 && string(header[:8]) == "\x89PNG\r\n\x1a\n":
		return "image/png"
	case len(header) >= 3 && header[0] == 0xff && header[1] == 0xd8 && header[2] == 0xff:
		return "image/jpeg"
	case len(header) >= 6 && (string(header[:6]) == "GIF87a" || string(header[:6]) == "GIF89a"):
		return "image/gif"
	case len(header) >= 12 && string(header[:4]) == "RIFF" && string(header[8:12]) == "WEBP":
		return "image/webp"
	default:
		return ""
	}
}

// ReadIfImage reports whether path is a supported image, and reads it only
// when it is. Non-images are left for the text reader to handle.
func ReadIfImage(path string) (*Image, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false, err
	}
	defer f.Close()
	var header [12]byte
	n, err := f.Read(header[:])
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, false, err
	}
	mime := MIME(header[:n])
	if mime == "" {
		return nil, false, nil
	}
	ref, err := Read(path)
	return ref, true, err
}

func Read(path string) (*Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() {
		return nil, fmt.Errorf("image: not a regular file")
	}
	if st.Size() > MaxBytes {
		return nil, fmt.Errorf("image: exceeds %d byte limit", MaxBytes)
	}
	data, err := io.ReadAll(io.LimitReader(f, MaxBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > MaxBytes {
		return nil, fmt.Errorf("image: exceeds %d byte limit", MaxBytes)
	}
	mime := MIME(data)
	if mime == "" {
		return nil, fmt.Errorf("image: unsupported format (use PNG, JPEG, GIF or WebP)")
	}
	hash := sha256.Sum256(data)
	return &Image{Path: path, MIMEType: mime, Size: int64(len(data)), SHA256: hex.EncodeToString(hash[:])}, nil
}

// DataURL rechecks the saved bytes before sending them to a vision model.
func (r Image) DataURL() (string, error) {
	if r.Path == "" || r.Size < 0 || r.Size > MaxBytes || !strings.HasPrefix(r.MIMEType, "image/") {
		return "", fmt.Errorf("image: invalid reference")
	}
	f, err := os.Open(r.Path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, MaxBytes+1))
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(data)
	if int64(len(data)) != r.Size || MIME(data) != r.MIMEType || hex.EncodeToString(hash[:]) != r.SHA256 {
		return "", fmt.Errorf("image: file changed since it was read")
	}
	return "data:" + r.MIMEType + ";base64," + base64.StdEncoding.EncodeToString(data), nil
}
