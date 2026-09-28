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
	maxSize       int64
	maxTotalSize  int64
	maxCount      int
	restrictTypes bool
	allowedTypes  []string
}

// NewHandler creates a new attachment handler
func NewHandler(cfg config.AttachmentConfig) *Handler {
	return &Handler{
		maxSize:      int64(cfg.MaxSize) * 1024 * 1024,
		maxTotalSize: int64(cfg.MaxTotalSize) * 1024 * 1024,
		maxCount:     cfg.MaxCount,
		// restrictTypes 取自原始配置而非归一化结果：用户配置了白名单就一定生效，
		// 即使条目全是无效值（如只填一个「.」）也按「拒绝所有」处理，不会因为
		// 归一化后列表变空而反转成「不限制」。
		restrictTypes: len(cfg.AllowedTypes) > 0,
		allowedTypes:  normalizeExtensions(cfg.AllowedTypes),
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
		// 体积限制适用于全部附件类型：除文本内容外，任何类型的上传都要受单文件
		// 上限约束，并计入总量。
		estimatedSize := int64(len(att.Content)) * 3 / 4
		if estimatedSize > h.maxSize {
			return &AttachmentError{
				Code:    ErrCodeSizeExceeded,
				Message: fmt.Sprintf("附件大小超过限制：%s (最大 %d MB，实际约 %d MB)", att.Name, h.maxSize/1024/1024, estimatedSize/1024/1024),
			}
		}
		totalSize += estimatedSize
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
	if !h.restrictTypes {
		return true
	}
	for _, allowed := range h.allowedTypes {
		if allowed == ext {
			return true
		}
	}
	return false
}

// normalizeExtensions 把白名单配置归一化为「不带前导点的小写扩展名」，与 Validate
// 中 filepath.Ext 去点后的形式对齐。
//
// 归一化在写入侧与消费侧各做一次。本函数是消费侧这一道，作用是兜住存量数据：
// 老 config.yaml 迁移进来的、或此前经 UI 写入的带点值，无需数据迁移脚本即可
// 正确匹配。
//
// 多段扩展名（如 .tar.gz）只去前导点，得到 tar.gz；而 filepath.Ext 只取最后
// 一段，实际比较时拿到的是 gz，两者不会相等，因此这类配置项不会命中。这是
// 已知限制，方向是拒绝而非放行，故不做特殊处理。
//
// nil 输入返回 nil。是否限制类型由 Handler.restrictTypes 承载，不依赖本函数的
// 返回长度。
func normalizeExtensions(types []string) []string {
	if types == nil {
		return nil
	}
	out := make([]string, 0, len(types))
	for _, t := range types {
		s := strings.ToLower(strings.TrimSpace(t))
		s = strings.TrimPrefix(s, ".")
		if s == "" {
			continue
		}
		out = append(out, s)
	}
	return out
}

// Attachment represents an incoming attachment
type Attachment struct {
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
}
