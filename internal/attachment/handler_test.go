package attachment

import (
	"testing"

	"github.com/zfd81/groot/internal/config"
)

func TestNewHandler(t *testing.T) {
	cfg := config.AttachmentConfig{
		MaxSize:      50,
		MaxTotalSize: 100,
		MaxCount:     10,
		AllowedTypes: []string{"pdf", "txt", "json"},
	}

	handler := NewHandler(cfg)
	if handler == nil {
		t.Fatal("NewHandler() returned nil")
	}

	if handler.maxCount != 10 {
		t.Errorf("maxCount = %d, want 10", handler.maxCount)
	}

	if handler.maxSize != 50*1024*1024 {
		t.Errorf("maxSize = %d, want %d", handler.maxSize, 50*1024*1024)
	}
}

func TestHandler_Validate_Empty(t *testing.T) {
	handler := NewHandler(config.AttachmentConfig{})

	err := handler.Validate([]Attachment{})
	if err != nil {
		t.Errorf("Validate() with empty attachments should not error: %v", err)
	}
}

func TestHandler_Validate_CountExceeded(t *testing.T) {
	handler := NewHandler(config.AttachmentConfig{MaxCount: 2})

	attachments := []Attachment{
		{Name: "file1.txt", Type: "file", Content: "Y29udGVudA=="},
		{Name: "file2.txt", Type: "file", Content: "Y29udGVudA=="},
		{Name: "file3.txt", Type: "file", Content: "Y29udGVudA=="},
	}

	err := handler.Validate(attachments)
	if err == nil {
		t.Error("Validate() should fail when count exceeds limit")
	}

	if attErr, ok := err.(*AttachmentError); ok {
		if attErr.Code != ErrCodeCountExceeded {
			t.Errorf("Error code = %s, want %s", attErr.Code, ErrCodeCountExceeded)
		}
	}
}

func TestHandler_Validate_MissingName(t *testing.T) {
	handler := NewHandler(config.AttachmentConfig{MaxCount: 10})

	attachments := []Attachment{
		{Name: "", Type: "file", Content: "Y29udGVudA=="},
	}

	err := handler.Validate(attachments)
	if err == nil {
		t.Error("Validate() should fail when name is missing")
	}

	if attErr, ok := err.(*AttachmentError); ok {
		if attErr.Code != ErrCodeMissingName {
			t.Errorf("Error code = %s, want %s", attErr.Code, ErrCodeMissingName)
		}
	}
}

func TestHandler_Validate_InvalidType(t *testing.T) {
	handler := NewHandler(config.AttachmentConfig{MaxCount: 10})

	attachments := []Attachment{
		{Name: "file.txt", Type: "invalid_type", Content: "Y29udGVudA=="},
	}

	err := handler.Validate(attachments)
	if err == nil {
		t.Error("Validate() should fail for invalid type")
	}

	if attErr, ok := err.(*AttachmentError); ok {
		if attErr.Code != ErrCodeInvalidType {
			t.Errorf("Error code = %s, want %s", attErr.Code, ErrCodeInvalidType)
		}
	}
}

func TestHandler_Validate_MissingContent(t *testing.T) {
	handler := NewHandler(config.AttachmentConfig{MaxCount: 10})

	attachments := []Attachment{
		{Name: "file.txt", Type: "file", Content: ""},
	}

	err := handler.Validate(attachments)
	if err == nil {
		t.Error("Validate() should fail when content is missing")
	}

	if attErr, ok := err.(*AttachmentError); ok {
		if attErr.Code != ErrCodeMissingContent {
			t.Errorf("Error code = %s, want %s", attErr.Code, ErrCodeMissingContent)
		}
	}
}

func TestHandler_Validate_TypeNotAllowed(t *testing.T) {
	handler := NewHandler(config.AttachmentConfig{
		MaxCount:     10,
		AllowedTypes: []string{"pdf", "txt"},
	})

	attachments := []Attachment{
		{Name: "file.exe", Type: "file", Content: "Y29udGVudA=="},
	}

	err := handler.Validate(attachments)
	if err == nil {
		t.Error("Validate() should fail for disallowed type")
	}

	if attErr, ok := err.(*AttachmentError); ok {
		if attErr.Code != ErrCodeTypeNotAllowed {
			t.Errorf("Error code = %s, want %s", attErr.Code, ErrCodeTypeNotAllowed)
		}
	}
}

func TestHandler_Validate_AllowedType(t *testing.T) {
	handler := NewHandler(config.AttachmentConfig{
		MaxCount:     10,
		MaxSize:      50,
		MaxTotalSize: 100,
		AllowedTypes: []string{"pdf", "txt"},
	})

	attachments := []Attachment{
		{Name: "file.txt", Type: "file", Content: "Y29udGVudA=="},
	}

	err := handler.Validate(attachments)
	if err != nil {
		t.Errorf("Validate() should pass for allowed type: %v", err)
	}
}

func TestHandler_Validate_NoRestriction(t *testing.T) {
	handler := NewHandler(config.AttachmentConfig{MaxCount: 10, MaxSize: 50, MaxTotalSize: 100})

	attachments := []Attachment{
		{Name: "file.xyz", Type: "file", Content: "Y29udGVudA=="},
	}

	err := handler.Validate(attachments)
	if err != nil {
		t.Errorf("Validate() should pass when no type restriction: %v", err)
	}
}

func TestHandler_Validate_SizeExceeded(t *testing.T) {
	handler := NewHandler(config.AttachmentConfig{
		MaxCount: 10,
		MaxSize:  1, // 1 MB
	})

	// 构造一个超过 1MB 的 base64 字符串（估算原始约 2MB）
	bigContent := make([]byte, 3*1024*1024) // base64 of 3MB raw → ~4MB string > 1MB limit
	for i := range bigContent {
		bigContent[i] = 'a'
	}

	attachments := []Attachment{
		{Name: "big.txt", Type: "file", Content: string(bigContent)},
	}

	err := handler.Validate(attachments)
	if err == nil {
		t.Error("Validate() should fail when size exceeds limit")
	}

	if attErr, ok := err.(*AttachmentError); ok {
		if attErr.Code != ErrCodeSizeExceeded {
			t.Errorf("Error code = %s, want %s", attErr.Code, ErrCodeSizeExceeded)
		}
	}
}

func TestAttachmentError_Error(t *testing.T) {
	err := &AttachmentError{
		Code:    "test_code",
		Message: "test message",
	}

	if err.Error() != "test message" {
		t.Errorf("Error() = %s, want test message", err.Error())
	}
}
