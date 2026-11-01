package media

// Attachment is a session-owned file supplied by the user. Path is an
// absolute path on the core host so tools can inspect non-image files.
type Attachment struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Path     string `json:"path"`
	Size     int64  `json:"size"`
	SHA256   string `json:"sha256"`
	MIMEType string `json:"mimeType"`
	Image    *Image `json:"image,omitempty"`
}
