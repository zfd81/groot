package attachment

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/zfd81/groot/internal/config"
)

// AttachmentError represents attachment validation error
type AttachmentError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *AttachmentError) Error() string {
	return e.Message
}

const (
	ErrCodeCountExceeded     = "attachment_count_exceeded"
	ErrCodeTypeNotAllowed    = "attachment_type_not_allowed"
	ErrCodeSizeExceeded      = "attachment_size_exceeded"
	ErrCodeTotalSizeExceeded = "attachment_total_size_exceeded"
	ErrCodeDecodeError       = "attachment_decode_error"
	ErrCodeMissingContent    = "attachment_missing_content"
	ErrCodeMissingName       = "attachment_missing_name"
	ErrCodeInvalidType       = "attachment_invalid_type"
)

// Handler handles attachment validation
type Handler struct {
	maxSize      int64
	maxTotalSize int64
	maxCount     int
	allowedTypes []string
}

// NewHandler creates a new attachment handler
func NewHandler(cfg config.AttachmentConfig) *Handler {
	return &Handler{
		maxSize:      int64(cfg.MaxSize) * 1024 * 1024,
		maxTotalSize: int64(cfg.MaxTotalSize) * 1024 * 1024,
		maxCount:     cfg.MaxCount,
		allowedTypes: cfg.AllowedTypes,
	}
}

// Validate validates attachments before processing
func (h *Handler) Validate(attachments []Attachment) error {
	if len(attachments) > h.maxCount {
		return &AttachmentError{
			Code:    ErrCodeCountExceeded,
			Message: fmt.Sprintf("附件数量超过限制：最大 %d 个，实际 %d 个", h.maxCount, len(attachments)),
		}
	}

	var totalSize int64
	for _, att := range attachments {
		if att.Name == "" {
			return &AttachmentError{Code: ErrCodeMissingName, Message: "附件缺少文件名"}
		}
		if att.Type != "file" && att.Type != "image" && att.Type != "audio" && att.Type != "video" {
			return &AttachmentError{Code: ErrCodeInvalidType, Message: fmt.Sprintf("无效的附件类型：%s", att.Type)}
		}
		if att.Content == "" {
			return &AttachmentError{Code: ErrCodeMissingContent, Message: fmt.Sprintf("附件 %s 缺少内容", att.Name)}
		}
		if att.Type == "file" || att.Type == "image" {
			ext := strings.ToLower(filepath.Ext(att.Name))
			if ext != "" {
				ext = ext[1:]
			}
			if !h.isTypeAllowed(ext) {
				return &AttachmentError{
					Code:    ErrCodeTypeNotAllowed,
					Message: fmt.Sprintf("附件类型不允许：%s (允许的类型：%s)", ext, strings.Join(h.allowedTypes, ", ")),
				}
			}
		}
		if att.Type == "file" && att.Content != "" {
			estimatedSize := int64(len(att.Content)) * 3 / 4
			if estimatedSize > h.maxSize {
				return &AttachmentError{
					Code:    ErrCodeSizeExceeded,
					Message: fmt.Sprintf("附件大小超过限制：%s (最大 %d MB，实际约 %d MB)", att.Name, h.maxSize/1024/1024, estimatedSize/1024/1024),
				}
			}
			totalSize += estimatedSize
		}
	}

	if totalSize > h.maxTotalSize {
		return &AttachmentError{
			Code:    ErrCodeTotalSizeExceeded,
			Message: fmt.Sprintf("附件总大小超过限制：最大 %d MB，实际约 %d MB", h.maxTotalSize/1024/1024, totalSize/1024/1024),
		}
	}
	return nil
}

func (h *Handler) isTypeAllowed(ext string) bool {
	if len(h.allowedTypes) == 0 {
		return true
	}
	for _, allowed := range h.allowedTypes {
		if strings.ToLower(allowed) == strings.ToLower(ext) {
			return true
		}
	}
	return false
}

// Attachment represents an incoming attachment
type Attachment struct {
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
}
