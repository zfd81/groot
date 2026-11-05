package handler

import (
	"net/http"
	"testing"

	"github.com/zfd81/groot/internal/api/errbody"
	"github.com/zfd81/groot/internal/attachment"
)

// TestAttachmentErrorCode 附件校验细分码到信封 code 的映射：
// 数量超限是请求本身不合法，只有体积超限才是 file_too_large。
func TestAttachmentErrorCode(t *testing.T) {
	cases := map[string]string{
		attachment.ErrCodeInvalidType:       errbody.CodeUnsupportedType,
		attachment.ErrCodeTypeNotAllowed:    errbody.CodeUnsupportedType,
		attachment.ErrCodeMissingName:       errbody.CodeInvalidRequest,
		attachment.ErrCodeMissingContent:    errbody.CodeInvalidRequest,
		attachment.ErrCodeDecodeError:       errbody.CodeInvalidRequest,
		attachment.ErrCodeCountExceeded:     errbody.CodeInvalidRequest,
		attachment.ErrCodeSizeExceeded:      errbody.CodeFileTooLarge,
		attachment.ErrCodeTotalSizeExceeded: errbody.CodeFileTooLarge,
		"unknown_code":                      errbody.CodeUnsupportedType,
	}
	for in, want := range cases {
		if got := attachmentErrorCode(in); got != want {
			t.Errorf("attachmentErrorCode(%s) = %s, want %s", in, got, want)
		}
	}
}

// TestAttachmentStatus 体积超限用 413，其余附件校验失败一律 400。
func TestAttachmentStatus(t *testing.T) {
	cases := map[string]int{
		errbody.CodeFileTooLarge:        http.StatusRequestEntityTooLarge,
		errbody.CodeInvalidRequest:      http.StatusBadRequest,
		errbody.CodeUnsupportedType:     http.StatusBadRequest,
		errbody.CodeUnsupportedFileType: http.StatusBadRequest,
	}
	for in, want := range cases {
		if got := attachmentStatus(in); got != want {
			t.Errorf("attachmentStatus(%s) = %d, want %d", in, got, want)
		}
	}
}
