package handler

import (
	"net/http"

	"github.com/zfd81/groot/internal/api/errbody"
	"github.com/zfd81/groot/internal/attachment"
)

// attachmentErrorCode 把附件校验的细分错误码归入对外信封的 code：
// 体积超限是 file_too_large；数量超限、缺文件名、缺内容、Base64 非法都属于请求本身不合法；
// 类型不被允许或无法识别属于「不支持的类型」。未知码按「不支持的类型」兜底。
//
// 附件模块的私有码只在服务端内部流转，任何对外响应都必须先经过本函数映射。
func attachmentErrorCode(code string) string {
	switch code {
	case attachment.ErrCodeSizeExceeded, attachment.ErrCodeTotalSizeExceeded:
		return errbody.CodeFileTooLarge
	case attachment.ErrCodeCountExceeded, attachment.ErrCodeMissingName,
		attachment.ErrCodeMissingContent, attachment.ErrCodeDecodeError:
		return errbody.CodeInvalidRequest
	case attachment.ErrCodeInvalidType, attachment.ErrCodeTypeNotAllowed:
		return errbody.CodeUnsupportedType
	default:
		return errbody.CodeUnsupportedType
	}
}

// attachmentStatus 给出信封 code 对应的 HTTP 状态码：只有 file_too_large 是 413，
// 其余附件校验失败都是请求本身不合法，返回 400。对话与图片解析两个调用点共用。
func attachmentStatus(code string) int {
	if code == errbody.CodeFileTooLarge {
		return http.StatusRequestEntityTooLarge
	}
	return http.StatusBadRequest
}
