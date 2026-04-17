package attachment

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zfd81/groot/internal/config"
)

// Handler handles attachment processing
type Handler struct {
	tempDir      string
	maxSize      int64 // in bytes
	maxTotalSize int64 // in bytes
	maxCount     int
	allowedTypes []string
}

// NewHandler creates a new attachment handler
func NewHandler(cfg config.AttachmentConfig, homeDir string) *Handler {
	tempDir := filepath.Join(homeDir, cfg.TempDirectory)

	// Ensure temp directory exists
	os.MkdirAll(tempDir, 0755)

	// Convert MB to bytes
	maxSize := int64(cfg.MaxSize) * 1024 * 1024
	maxTotalSize := int64(cfg.MaxTotalSize) * 1024 * 1024

	return &Handler{
		tempDir:      tempDir,
		maxSize:      maxSize,
		maxTotalSize: maxTotalSize,
		maxCount:     cfg.MaxCount,
		allowedTypes: cfg.AllowedTypes,
	}
}

// ProcessedAttachment represents a processed attachment
type ProcessedAttachment struct {
	OriginalName string    // Original file name
	Type         string    // Attachment type (file, url)
	Path         string    // Saved file path (relative to taskDir)
	FullPath     string    // Absolute path
	Size         int64     // File size in bytes
	ContentType  string    // Content type based on extension
	SavedAt      time.Time // When it was saved
}

// Validate validates attachments before processing
func (h *Handler) Validate(attachments []Attachment) error {
	if len(attachments) > h.maxCount {
		return fmt.Errorf("附件数量超过限制：最大 %d 个，实际 %d 个", h.maxCount, len(attachments))
	}

	var totalSize int64
	for _, att := range attachments {
		// Check type
		ext := strings.ToLower(filepath.Ext(att.Name))
		if ext != "" {
			ext = ext[1:] // Remove leading dot
		}
		if !h.isTypeAllowed(ext) {
			return fmt.Errorf("附件类型不允许：%s (允许的类型：%s)", ext, strings.Join(h.allowedTypes, ", "))
		}

		// Check size (estimate from Base64 content length)
		if att.Type == "file" && att.Content != "" {
			// Base64 encoding increases size by ~33%
			estimatedSize := int64(len(att.Content)) * 3 / 4
			if estimatedSize > h.maxSize {
				return fmt.Errorf("附件大小超过限制：%s (最大 %d MB，实际约 %d MB)",
					att.Name, h.maxSize/1024/1024, estimatedSize/1024/1024)
			}
			totalSize += estimatedSize
		}
	}

	if totalSize > h.maxTotalSize {
		return fmt.Errorf("附件总大小超过限制：最大 %d MB，实际约 %d MB",
			h.maxTotalSize/1024/1024, totalSize/1024/1024)
	}

	return nil
}

// Process processes attachments for a task
// Returns processed attachments with file paths
func (h *Handler) Process(taskID string, attachments []Attachment) ([]ProcessedAttachment, error) {
	if len(attachments) == 0 {
		return nil, nil
	}

	// Create task-specific directory
	taskDir := filepath.Join(h.tempDir, taskID)
	if err := os.MkdirAll(taskDir, 0755); err != nil {
		return nil, fmt.Errorf("创建临时目录失败：%w", err)
	}

	results := make([]ProcessedAttachment, 0, len(attachments))

	for _, att := range attachments {
		result, err := h.processSingle(taskDir, att)
		if err != nil {
			// Cleanup on error
			h.cleanupDir(taskDir)
			return nil, fmt.Errorf("处理附件 %s 失败：%w", att.Name, err)
		}
		results = append(results, *result)
	}

	return results, nil
}

// processSingle processes a single attachment
func (h *Handler) processSingle(taskDir string, att Attachment) (*ProcessedAttachment, error) {
	result := &ProcessedAttachment{
		OriginalName: att.Name,
		Type:         att.Type,
		SavedAt:      time.Now(),
	}

	switch att.Type {
	case "file", "image":
		// Decode Base64 content
		content, err := base64.StdEncoding.DecodeString(att.Content)
		if err != nil {
			return nil, fmt.Errorf("Base64 解码失败：%w", err)
		}

		// Generate safe filename
		safeName := sanitizeFilename(att.Name)
		fullPath := filepath.Join(taskDir, safeName)

		// Write file
		if err := os.WriteFile(fullPath, content, 0644); err != nil {
			return nil, fmt.Errorf("写入文件失败：%w", err)
		}

		result.FullPath = fullPath
		result.Path = safeName
		result.Size = int64(len(content))
		result.ContentType = getContentType(att.Name)

	case "url":
		// URL type - just record the URL, no file saving
		result.Path = att.Content // URL is stored in Content field
		result.FullPath = att.Content
		result.Size = 0
		result.ContentType = "url"

	case "text":
		// Text type - store content directly, no file saving
		result.Path = ""
		result.FullPath = ""
		result.Size = int64(len(att.Content))
		result.ContentType = "text/plain"

	default:
		return nil, fmt.Errorf("未知的附件类型：%s", att.Type)
	}

	return result, nil
}

// Cleanup cleans up temporary files for a task
func (h *Handler) Cleanup(taskID string) error {
	taskDir := filepath.Join(h.tempDir, taskID)
	return h.cleanupDir(taskDir)
}

// cleanupDir removes a directory and its contents
func (h *Handler) cleanupDir(dir string) error {
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return nil // Already gone
	}
	return os.RemoveAll(dir)
}

// GetTempDir returns the temporary directory path
func (h *Handler) GetTempDir() string {
	return h.tempDir
}

// isTypeAllowed checks if a file type is allowed
func (h *Handler) isTypeAllowed(ext string) bool {
	if len(h.allowedTypes) == 0 {
		return true // No restriction
	}
	for _, allowed := range h.allowedTypes {
		if strings.ToLower(allowed) == strings.ToLower(ext) {
			return true
		}
	}
	return false
}

// sanitizeFilename sanitizes a filename for safe storage
func sanitizeFilename(name string) string {
	// Replace unsafe characters
	name = strings.ReplaceAll(name, "/", "_")
	name = strings.ReplaceAll(name, "\\", "_")
	name = strings.ReplaceAll(name, "..", "_")

	// Limit length
	if len(name) > 255 {
		ext := filepath.Ext(name)
		base := name[:255-len(ext)]
		name = base + ext
	}

	return name
}

// getContentType returns content type based on file extension
func getContentType(filename string) string {
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".pdf":
		return "application/pdf"
	case ".doc":
		return "application/msword"
	case ".docx":
		return "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	case ".txt":
		return "text/plain"
	case ".json":
		return "application/json"
	case ".csv":
		return "text/csv"
	case ".xml":
		return "application/xml"
	case ".yaml", ".yml":
		return "application/x-yaml"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".zip":
		return "application/zip"
	case ".tar":
		return "application/x-tar"
	default:
		return "application/octet-stream"
	}
}

// Attachment represents an incoming attachment
type Attachment struct {
	Type    string `json:"type"`    // file, image, url, text
	Name    string `json:"name"`    // filename
	Content string `json:"content"` // Base64 content or URL
}