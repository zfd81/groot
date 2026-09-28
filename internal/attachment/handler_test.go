package attachment

import (
	"errors"
	"strings"
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

// TestHandler_Validate_DottedAllowedTypes 验证带点号的白名单配置（UI 提示与
// README 的格式）能正确匹配。归一化前这里必然失败：ext 已去点，配置未去点。
func TestHandler_Validate_DottedAllowedTypes(t *testing.T) {
	handler := NewHandler(config.AttachmentConfig{
		MaxSize:      50,
		MaxTotalSize: 100,
		MaxCount:     10,
		AllowedTypes: []string{".png", ".pdf"},
	})

	err := handler.Validate([]Attachment{
		{Type: "file", Name: "doc.pdf", Content: "abc"},
	})
	if err != nil {
		t.Errorf("带点白名单应放行同类型附件，却报错: %v", err)
	}
}

// TestHandler_Validate_MixedCaseAllowedTypes 验证白名单大小写与前导点混用时
// 仍能匹配，且不匹配的类型依然被拒。
func TestHandler_Validate_MixedCaseAllowedTypes(t *testing.T) {
	handler := NewHandler(config.AttachmentConfig{
		MaxSize:      50,
		MaxTotalSize: 100,
		MaxCount:     10,
		AllowedTypes: []string{".PNG", "PDF", "  .Txt  "},
	})

	for _, name := range []string{"a.png", "b.pdf", "c.txt", "d.PNG"} {
		if err := handler.Validate([]Attachment{
			{Type: "file", Name: name, Content: "abc"},
		}); err != nil {
			t.Errorf("%s 应被放行，却报错: %v", name, err)
		}
	}

	// 白名单同样作用于 image 分支，覆盖这条路径。
	if err := handler.Validate([]Attachment{
		{Type: "image", Name: "a.PNG", Content: "abc"},
	}); err != nil {
		t.Errorf("image 类型 a.PNG 应被放行，却报错: %v", err)
	}

	err := handler.Validate([]Attachment{
		{Type: "file", Name: "e.exe", Content: "abc"},
	})
	if err == nil {
		t.Error("exe 不在白名单内，应被拒绝")
	}
}

// TestNormalizeExtensions 验证归一化：去空白、去前导点、转小写、丢弃空项。
func TestNormalizeExtensions(t *testing.T) {
	got := normalizeExtensions([]string{" .PNG ", "PDF", ".", "", "  ", ".tar.gz"})
	// ".tar.gz" 归一化为 "tar.gz" 只是「去前导点」的直接结果，并不意味着多段
	// 扩展名受支持：Validate 里 filepath.Ext 只取最后一段（gz），永远匹配不上
	// tar.gz。这是已知限制，方向是拒绝而非放行，故不做特殊处理。
	want := []string{"png", "pdf", "tar.gz"}
	if len(got) != len(want) {
		t.Fatalf("归一化结果 = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("归一化结果[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestNormalizeExtensions_NilStaysNil 验证 nil 输入原样返回 nil，不额外分配。
// 不限制类型的语义由 Handler.restrictTypes 承载，不由本函数的返回值表达，
// 见 TestHandler_Validate_NoRestriction。
func TestNormalizeExtensions_NilStaysNil(t *testing.T) {
	if got := normalizeExtensions(nil); got != nil {
		t.Errorf("normalizeExtensions(nil) = %v, want nil", got)
	}
}

// TestHandler_Validate_AllInvalidTypesDenyAll 验证白名单条目全部无效（归一化后
// 为空）时，仍按「用户配置了限制」处理，拒绝所有附件，而非反转成不限制。
func TestHandler_Validate_AllInvalidTypesDenyAll(t *testing.T) {
	handler := NewHandler(config.AttachmentConfig{
		MaxSize:      50,
		MaxTotalSize: 100,
		MaxCount:     10,
		AllowedTypes: []string{".", "  "},
	})

	if err := handler.Validate([]Attachment{
		{Type: "file", Name: "a.png", Content: "abc"},
	}); err == nil {
		t.Error("白名单条目全无效时应拒绝附件，不应放行")
	}
}

// TestHandler_Validate_NonFileSizeExceeded 验证 image/audio/video 三类附件
// 同样受单文件上限约束。修复前它们完全绕过体积校验。
func TestHandler_Validate_NonFileSizeExceeded(t *testing.T) {
	handler := NewHandler(config.AttachmentConfig{
		MaxSize:      1,
		MaxTotalSize: 100,
		MaxCount:     10,
	})

	// base64 长度 * 3/4 约等于原始字节数；取 4MB 字面量得约 3MB，确保超过 1MB 上限
	big := strings.Repeat("A", 4*1024*1024)

	for _, typ := range []string{"image", "audio", "video"} {
		err := handler.Validate([]Attachment{
			{Type: typ, Name: "big.dat", Content: big},
		})
		if err == nil {
			t.Errorf("%s 类型超过单文件上限，应被拒绝", typ)
			continue
		}
		var attErr *AttachmentError
		if !errors.As(err, &attErr) || attErr.Code != ErrCodeSizeExceeded {
			t.Errorf("%s 类型应返回 %s，实际: %v", typ, ErrCodeSizeExceeded, err)
		}
	}
}

// TestHandler_Validate_NonFileCountsTowardTotal 验证非 file 类附件计入总量。
// 三个各约 3MB 的图片，单个都不超 5MB 上限，合计超过 6MB 总量上限。
func TestHandler_Validate_NonFileCountsTowardTotal(t *testing.T) {
	handler := NewHandler(config.AttachmentConfig{
		MaxSize:      5,
		MaxTotalSize: 6,
		MaxCount:     10,
	})

	img := strings.Repeat("A", 4*1024*1024) // 约 3MB
	err := handler.Validate([]Attachment{
		{Type: "image", Name: "a.png", Content: img},
		{Type: "image", Name: "b.png", Content: img},
		{Type: "image", Name: "c.png", Content: img},
	})
	if err == nil {
		t.Fatal("三个图片合计超过总量上限，应被拒绝")
	}
	var attErr *AttachmentError
	if !errors.As(err, &attErr) || attErr.Code != ErrCodeTotalSizeExceeded {
		t.Errorf("应返回 %s，实际: %v", ErrCodeTotalSizeExceeded, err)
	}
}

// TestHandler_Validate_NonFileWithinLimit 验证限内的非 file 附件仍放行，
// 确认修复没有误伤正常路径。
func TestHandler_Validate_NonFileWithinLimit(t *testing.T) {
	handler := NewHandler(config.AttachmentConfig{
		MaxSize:      50,
		MaxTotalSize: 100,
		MaxCount:     10,
	})

	for _, typ := range []string{"image", "audio", "video", "file"} {
		if err := handler.Validate([]Attachment{
			{Type: typ, Name: "small.dat", Content: "abcd"},
		}); err != nil {
			t.Errorf("%s 类型在限内应放行，却报错: %v", typ, err)
		}
	}
}
