package attachment

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zfd81/groot/internal/config"
)

func TestNewHandler(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := config.AttachmentConfig{
		MaxSize:      50,
		MaxTotalSize: 100,
		MaxCount:     10,
		AllowedTypes: []string{"pdf", "txt", "json"},
	}

	handler := NewHandler(cfg, tmpDir)
	if handler == nil {
		t.Fatal("NewHandler() returned nil")
	}

	// 验证 temp 目录已创建
	tempDir := filepath.Join(tmpDir, "temp")
	if _, err := os.Stat(tempDir); os.IsNotExist(err) {
		t.Error("NewHandler() 未创建 temp 目录")
	}

	// 验证配置值
	if handler.maxCount != 10 {
		t.Errorf("maxCount = %d, want 10", handler.maxCount)
	}

	// 验证字节转换 (MB to bytes)
	if handler.maxSize != 50*1024*1024 {
		t.Errorf("maxSize = %d, want %d", handler.maxSize, 50*1024*1024)
	}
}

func TestHandler_Validate_Empty(t *testing.T) {
	tmpDir := t.TempDir()
	handler := NewHandler(config.AttachmentConfig{}, tmpDir)

	err := handler.Validate([]Attachment{})
	if err != nil {
		t.Errorf("Validate() with empty attachments should not error: %v", err)
	}
}

func TestHandler_Validate_CountExceeded(t *testing.T) {
	tmpDir := t.TempDir()
	handler := NewHandler(config.AttachmentConfig{MaxCount: 2}, tmpDir)

	attachments := []Attachment{
		{Name: "file1.txt", Type: "file", Content: "aGVsbG8="},
		{Name: "file2.txt", Type: "file", Content: "aGVsbG8="},
		{Name: "file3.txt", Type: "file", Content: "aGVsbG8="},
	}

	err := handler.Validate(attachments)
	if err == nil {
		t.Error("Validate() should fail for count exceeded")
	}

	attErr, ok := err.(*AttachmentError)
	if !ok {
		t.Errorf("Error should be AttachmentError, got %T", err)
	}

	if attErr.Code != ErrCodeCountExceeded {
		t.Errorf("Error code = %s, want %s", attErr.Code, ErrCodeCountExceeded)
	}
}

func TestHandler_Validate_MissingName(t *testing.T) {
	tmpDir := t.TempDir()
	handler := NewHandler(config.AttachmentConfig{MaxCount: 10}, tmpDir)

	attachments := []Attachment{
		{Type: "file", Content: "aGVsbG8="}, // 缺少 Name
	}

	err := handler.Validate(attachments)
	if err == nil {
		t.Error("Validate() should fail for missing name")
	}

	attErr, ok := err.(*AttachmentError)
	if !ok {
		t.Fatalf("Error should be AttachmentError, got %T", err)
	}

	if attErr.Code != ErrCodeMissingName {
		t.Errorf("Error code = %s, want %s", attErr.Code, ErrCodeMissingName)
	}
}

func TestHandler_Validate_InvalidType(t *testing.T) {
	tmpDir := t.TempDir()
	handler := NewHandler(config.AttachmentConfig{MaxCount: 10}, tmpDir)

	attachments := []Attachment{
		{Name: "file.xyz", Type: "invalid_type", Content: "aGVsbG8="},
	}

	err := handler.Validate(attachments)
	if err == nil {
		t.Error("Validate() should fail for invalid type")
	}

	attErr, ok := err.(*AttachmentError)
	if !ok {
		t.Fatalf("Error should be AttachmentError, got %T", err)
	}

	if attErr.Code != ErrCodeInvalidType {
		t.Errorf("Error code = %s, want %s", attErr.Code, ErrCodeInvalidType)
	}
}

func TestHandler_Validate_MissingContent(t *testing.T) {
	tmpDir := t.TempDir()
	handler := NewHandler(config.AttachmentConfig{MaxCount: 10}, tmpDir)

	attachments := []Attachment{
		{Name: "file.txt", Type: "file"}, // 缺少 Content
	}

	err := handler.Validate(attachments)
	if err == nil {
		t.Error("Validate() should fail for missing content")
	}

	attErr, ok := err.(*AttachmentError)
	if !ok {
		t.Fatalf("Error should be AttachmentError, got %T", err)
	}

	if attErr.Code != ErrCodeMissingContent {
		t.Errorf("Error code = %s, want %s", attErr.Code, ErrCodeMissingContent)
	}
}

func TestHandler_Validate_TypeNotAllowed(t *testing.T) {
	tmpDir := t.TempDir()
	handler := NewHandler(config.AttachmentConfig{
		MaxCount:     10,
		AllowedTypes: []string{"pdf", "txt"},
	}, tmpDir)

	attachments := []Attachment{
		{Name: "file.exe", Type: "file", Content: "aGVsbG8="},
	}

	err := handler.Validate(attachments)
	if err == nil {
		t.Error("Validate() should fail for type not allowed")
	}

	attErr, ok := err.(*AttachmentError)
	if !ok {
		t.Fatalf("Error should be AttachmentError, got %T", err)
	}

	if attErr.Code != ErrCodeTypeNotAllowed {
		t.Errorf("Error code = %s, want %s", attErr.Code, ErrCodeTypeNotAllowed)
	}
}

func TestHandler_Validate_AllowedType(t *testing.T) {
	tmpDir := t.TempDir()
	handler := NewHandler(config.AttachmentConfig{
		MaxSize:      50, // 设置足够大的 MaxSize
		MaxTotalSize: 100,
		MaxCount:     10,
		AllowedTypes: []string{"pdf", "txt"},
	}, tmpDir)

	attachments := []Attachment{
		{Name: "file.txt", Type: "file", Content: "aGVsbG8="},
		{Name: "file.pdf", Type: "file", Content: "aGVsbG8="},
	}

	err := handler.Validate(attachments)
	if err != nil {
		t.Errorf("Validate() should pass for allowed types: %v", err)
	}
}

func TestHandler_Validate_NoRestriction(t *testing.T) {
	tmpDir := t.TempDir()
	handler := NewHandler(config.AttachmentConfig{
		MaxSize:      50, // 设置足够大的 MaxSize
		MaxTotalSize: 100,
		MaxCount:     10,
		AllowedTypes: []string{}, // 空数组表示无限制
	}, tmpDir)

	attachments := []Attachment{
		{Name: "file.xyz", Type: "file", Content: "aGVsbG8="},
	}

	err := handler.Validate(attachments)
	if err != nil {
		t.Errorf("Validate() should pass with no type restriction: %v", err)
	}
}

func TestHandler_Validate_SizeExceeded(t *testing.T) {
	tmpDir := t.TempDir()
	handler := NewHandler(config.AttachmentConfig{
		MaxSize:  1, // 1 MB
		MaxCount: 10,
	}, tmpDir)

	// 创建一个超过 1 MB 的 Base64 内容 (约 1.5 MB 原始数据)
	largeContent := strings.Repeat("a", 2*1024*1024) // 2MB Base64 ≈ 1.5MB 原始

	attachments := []Attachment{
		{Name: "large.txt", Type: "file", Content: largeContent},
	}

	err := handler.Validate(attachments)
	if err == nil {
		t.Error("Validate() should fail for size exceeded")
	}

	attErr, ok := err.(*AttachmentError)
	if !ok {
		t.Fatalf("Error should be AttachmentError, got %T", err)
	}

	if attErr.Code != ErrCodeSizeExceeded {
		t.Errorf("Error code = %s, want %s", attErr.Code, ErrCodeSizeExceeded)
	}
}

func TestHandler_Validate_URLType(t *testing.T) {
	tmpDir := t.TempDir()
	handler := NewHandler(config.AttachmentConfig{MaxCount: 10}, tmpDir)

	attachments := []Attachment{
		{Name: "link", Type: "url", Content: "https://example.com"},
	}

	err := handler.Validate(attachments)
	if err != nil {
		t.Errorf("Validate() should pass for URL type: %v", err)
	}
}

func TestHandler_Validate_TextType(t *testing.T) {
	tmpDir := t.TempDir()
	handler := NewHandler(config.AttachmentConfig{MaxCount: 10}, tmpDir)

	attachments := []Attachment{
		{Name: "text", Type: "text", Content: "hello world"},
	}

	err := handler.Validate(attachments)
	if err != nil {
		t.Errorf("Validate() should pass for text type: %v", err)
	}
}

func TestHandler_Process_Empty(t *testing.T) {
	tmpDir := t.TempDir()
	handler := NewHandler(config.AttachmentConfig{}, tmpDir)

	results, err := handler.Process("task_001", []Attachment{})
	if err != nil {
		t.Errorf("Process() with empty attachments should not error: %v", err)
	}

	if results != nil {
		t.Error("Process() with empty attachments should return nil")
	}
}

func TestHandler_Process_File(t *testing.T) {
	tmpDir := t.TempDir()
	handler := NewHandler(config.AttachmentConfig{MaxCount: 10}, tmpDir)

	content := "hello world"
	encoded := base64.StdEncoding.EncodeToString([]byte(content))

	attachments := []Attachment{
		{Name: "test.txt", Type: "file", Content: encoded},
	}

	results, err := handler.Process("task_001", attachments)
	if err != nil {
		t.Fatalf("Process() failed: %v", err)
	}

	if len(results) != 1 {
		t.Errorf("Process() returned %d results, want 1", len(results))
	}

	// 验证文件已保存
	if _, err := os.Stat(results[0].FullPath); os.IsNotExist(err) {
		t.Error("Process() should save file")
	}

	if results[0].OriginalName != "test.txt" {
		t.Errorf("OriginalName = %s, want test.txt", results[0].OriginalName)
	}

	if results[0].ContentType != "text/plain" {
		t.Errorf("ContentType = %s, want text/plain", results[0].ContentType)
	}

	if results[0].Size != int64(len(content)) {
		t.Errorf("Size = %d, want %d", results[0].Size, len(content))
	}
}

func TestHandler_Process_Image(t *testing.T) {
	tmpDir := t.TempDir()
	handler := NewHandler(config.AttachmentConfig{MaxCount: 10}, tmpDir)

	content := "fake image data"
	encoded := base64.StdEncoding.EncodeToString([]byte(content))

	attachments := []Attachment{
		{Name: "image.png", Type: "image", Content: encoded},
	}

	results, err := handler.Process("task_002", attachments)
	if err != nil {
		t.Fatalf("Process() failed: %v", err)
	}

	if results[0].ContentType != "image/png" {
		t.Errorf("ContentType = %s, want image/png", results[0].ContentType)
	}
}

func TestHandler_Process_URL(t *testing.T) {
	tmpDir := t.TempDir()
	handler := NewHandler(config.AttachmentConfig{MaxCount: 10}, tmpDir)

	attachments := []Attachment{
		{Name: "link", Type: "url", Content: "https://example.com"},
	}

	results, err := handler.Process("task_003", attachments)
	if err != nil {
		t.Fatalf("Process() failed: %v", err)
	}

	if results[0].Path != "https://example.com" {
		t.Errorf("Path = %s, want https://example.com", results[0].Path)
	}

	if results[0].ContentType != "url" {
		t.Errorf("ContentType = %s, want url", results[0].ContentType)
	}
}

func TestHandler_Process_Text(t *testing.T) {
	tmpDir := t.TempDir()
	handler := NewHandler(config.AttachmentConfig{MaxCount: 10}, tmpDir)

	attachments := []Attachment{
		{Name: "text", Type: "text", Content: "hello world"},
	}

	results, err := handler.Process("task_004", attachments)
	if err != nil {
		t.Fatalf("Process() failed: %v", err)
	}

	if results[0].ContentType != "text/plain" {
		t.Errorf("ContentType = %s, want text/plain", results[0].ContentType)
	}

	if results[0].Size != int64(len("hello world")) {
		t.Errorf("Size = %d, want %d", results[0].Size, len("hello world"))
	}
}

func TestHandler_Process_InvalidBase64(t *testing.T) {
	tmpDir := t.TempDir()
	handler := NewHandler(config.AttachmentConfig{MaxCount: 10, MaxSize: 50}, tmpDir)

	attachments := []Attachment{
		{Name: "test.txt", Type: "file", Content: "invalid!!!base64"},
	}

	_, err := handler.Process("task_005", attachments)
	if err == nil {
		t.Error("Process() should fail for invalid Base64")
	}

	// 错误可能被包装，检查错误消息包含 decode error
	if !strings.Contains(err.Error(), "Base64 解码失败") {
		t.Errorf("Error should contain Base64 decode error message, got: %v", err)
	}
}

func TestHandler_Cleanup(t *testing.T) {
	tmpDir := t.TempDir()
	handler := NewHandler(config.AttachmentConfig{MaxCount: 10}, tmpDir)

	// 先处理一个附件
	encoded := base64.StdEncoding.EncodeToString([]byte("test"))
	attachments := []Attachment{
		{Name: "test.txt", Type: "file", Content: encoded},
	}

	handler.Process("task_cleanup", attachments)

	// 验证目录存在
	taskDir := filepath.Join(handler.GetTempDir(), "task_cleanup")
	if _, err := os.Stat(taskDir); os.IsNotExist(err) {
		t.Fatal("Task directory should exist after Process()")
	}

	// 执行清理
	err := handler.Cleanup("task_cleanup")
	if err != nil {
		t.Fatalf("Cleanup() failed: %v", err)
	}

	// 验证目录已删除
	if _, err := os.Stat(taskDir); !os.IsNotExist(err) {
		t.Error("Cleanup() should remove task directory")
	}
}

func TestHandler_Cleanup_Nonexistent(t *testing.T) {
	tmpDir := t.TempDir()
	handler := NewHandler(config.AttachmentConfig{}, tmpDir)

	// 清理不存在的任务不应报错
	err := handler.Cleanup("nonexistent_task")
	if err != nil {
		t.Errorf("Cleanup() for nonexistent task should not error: %v", err)
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

func TestSanitizeFilename(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "正常文件名",
			input:    "test.txt",
			expected: "test.txt",
		},
		{
			name:     "包含斜杠",
			input:    "path/test.txt",
			expected: "path_test.txt",
		},
		{
			name:     "包含反斜杠",
			input:    "path\\test.txt",
			expected: "path_test.txt",
		},
		{
			name:     "包含双点",
			input:    "../test.txt",
			expected: "__test.txt",
		},
		{
			name:     "过长文件名",
			input:    strings.Repeat("a", 300) + ".txt",
			expected: strings.Repeat("a", 251) + ".txt",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := sanitizeFilename(tt.input)
			if result != tt.expected {
				t.Errorf("sanitizeFilename(%s) = %s, want %s", tt.input, result, tt.expected)
			}
		})
	}
}

func TestGetContentType(t *testing.T) {
	tests := []struct {
		filename string
		expected string
	}{
		{"file.pdf", "application/pdf"},
		{"file.txt", "text/plain"},
		{"file.json", "application/json"},
		{"file.csv", "text/csv"},
		{"file.xml", "application/xml"},
		{"file.yaml", "application/x-yaml"},
		{"file.yml", "application/x-yaml"},
		{"file.png", "image/png"},
		{"file.jpg", "image/jpeg"},
		{"file.jpeg", "image/jpeg"},
		{"file.zip", "application/zip"},
		{"file.tar", "application/x-tar"},
		{"file.unknown", "application/octet-stream"},
	}

	for _, tt := range tests {
		t.Run(tt.filename, func(t *testing.T) {
			result := getContentType(tt.filename)
			if result != tt.expected {
				t.Errorf("getContentType(%s) = %s, want %s", tt.filename, result, tt.expected)
			}
		})
	}
}

func TestHandler_GetTempDir(t *testing.T) {
	tmpDir := t.TempDir()
	handler := NewHandler(config.AttachmentConfig{}, tmpDir)

	expected := filepath.Join(tmpDir, "temp")
	if handler.GetTempDir() != expected {
		t.Errorf("GetTempDir() = %s, want %s", handler.GetTempDir(), expected)
	}
}

func TestHandler_Process_Multiple(t *testing.T) {
	tmpDir := t.TempDir()
	handler := NewHandler(config.AttachmentConfig{MaxCount: 10}, tmpDir)

	encoded1 := base64.StdEncoding.EncodeToString([]byte("content1"))
	encoded2 := base64.StdEncoding.EncodeToString([]byte("content2"))

	attachments := []Attachment{
		{Name: "file1.txt", Type: "file", Content: encoded1},
		{Name: "file2.txt", Type: "file", Content: encoded2},
		{Name: "link", Type: "url", Content: "https://example.com"},
	}

	results, err := handler.Process("task_multi", attachments)
	if err != nil {
		t.Fatalf("Process() failed: %v", err)
	}

	if len(results) != 3 {
		t.Errorf("Process() returned %d results, want 3", len(results))
	}

	// 验证所有文件已保存
	for i, r := range results {
		if i < 2 { // file 类型
			if _, err := os.Stat(r.FullPath); os.IsNotExist(err) {
				t.Errorf("File %d should be saved", i)
			}
		}
	}
}