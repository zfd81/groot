package attachment

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Handler processes file attachments
type Handler struct {
	tempDir string
}

// NewHandler creates a new attachment handler
func NewHandler(tempDir string) *Handler {
	// Ensure temp directory exists
	os.MkdirAll(tempDir, 0755)
	return &Handler{tempDir: tempDir}
}

// Process processes an attachment and returns processed result
func (h *Handler) Process(att Attachment) (*ProcessedAttachment, error) {
	result := &ProcessedAttachment{
		Type: att.Type,
		Name: att.Name,
	}

	switch att.Type {
	case "image", "file":
		// Decode base64 content
		data, err := base64.StdEncoding.DecodeString(att.Content)
		if err != nil {
			return nil, fmt.Errorf("failed to decode content: %w", err)
		}

		// Generate unique temp filename
		ext := filepath.Ext(att.Name)
		if ext == "" {
			ext = ".bin"
		}
		tempPath := filepath.Join(h.tempDir, fmt.Sprintf("%d_%s", time.Now().UnixNano(), filepath.Base(att.Name)))

		// Write to temp file
		if err := os.WriteFile(tempPath, data, 0644); err != nil {
			return nil, fmt.Errorf("failed to save file: %w", err)
		}

		result.Path = tempPath
		result.Size = len(data)

	case "url":
		// URL attachment - just store the URL reference
		result.URL = att.Content
		result.Size = 0

	case "text":
		// Text attachment - store content directly
		result.Content = att.Content
		result.Size = len(att.Content)

	default:
		return nil, fmt.Errorf("unknown attachment type: %s", att.Type)
	}

	return result, nil
}

// Cleanup removes a processed attachment's temp file
func (h *Handler) Cleanup(path string) error {
	if path != "" && filepath.Dir(path) == h.tempDir {
		return os.Remove(path)
	}
	return nil
}

// CleanupAll removes all temp files
func (h *Handler) CleanupAll() error {
	return os.RemoveAll(h.tempDir)
}

// Attachment represents input attachment
type Attachment struct {
	Type    string `json:"type"`    // file, image, url, text
	Name    string `json:"name"`    // filename
	Content string `json:"content"` // base64 content or URL
}

// ProcessedAttachment represents processed attachment result
type ProcessedAttachment struct {
	Type    string `json:"type"`
	Name    string `json:"name"`
	Path    string `json:"path,omitempty"`    // temp file path
	URL     string `json:"url,omitempty"`     // URL reference
	Content string `json:"content,omitempty"` // text content
	Size    int    `json:"size"`              // size in bytes
}