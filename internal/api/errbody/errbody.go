package errbody

import (
	"encoding/json"
	"errors"

	"github.com/cloudwego/hertz/pkg/app"
)

// ErrBodyTooLarge 表示请求体读取过程中超过硬上限；由 BodyLimit 包装的请求体返回，
// 代理层据此映射为 413 file_too_large。
var ErrBodyTooLarge = errors.New("request body too large")

// Envelope 是 OpenAI 错误响应的完整响应体。
type Envelope struct {
	Error Error `json:"error"`
}

// Error 是信封内的 error 对象；Param 与 Code 无值时序列化为 null。
type Error struct {
	Message string  `json:"message"`
	Type    string  `json:"type"`
	Param   *string `json:"param"`
	Code    *string `json:"code"`
}

// 信封 type 取值
const (
	TypeInvalidRequest = "invalid_request_error"
	TypeAuthentication = "authentication_error"
	TypePermission     = "permission_error"
	TypeRateLimit      = "rate_limit_error"
	TypeAPI            = "api_error"
)

// 信封 code 取值
const (
	CodeInvalidRequest          = "invalid_request"
	CodeModelNotFound           = "model_not_found"
	CodeUnsupportedFileType     = "unsupported_file_type"
	CodeUnsupportedType         = "unsupported_type"
	CodeInvalidAPIKey           = "invalid_api_key"
	CodeInsufficientPermissions = "insufficient_permissions"
	CodeTenantNotBound          = "tenant_not_bound"
	CodeSessionNotFound         = "session_not_found"
	CodeTaskNotFound            = "task_not_found"
	CodeScheduleNotFound        = "schedule_not_found"
	CodeChatLimitExceeded       = "chat_limit_exceeded"
	CodeChatStillRunning        = "chat_still_running"
	CodeFileTooLarge            = "file_too_large"
	CodeRateLimitExceeded       = "rate_limit_exceeded"
	CodeClientClosedRequest     = "client_closed_request"
	CodeInternalError           = "internal_error"
	CodeUpstreamError           = "upstream_error"
	CodeScheduleUnavailable     = "schedule_unavailable"
	CodeServiceUnavailable      = "service_unavailable"
	CodeUnknownAgent            = "unknown_agent"
)

// New 构造信封；param 与 code 传空串表示 JSON null。
func New(message, typ, param, code string) Envelope {
	return Envelope{Error: Error{
		Message: message,
		Type:    typ,
		Param:   optional(param),
		Code:    optional(code),
	}}
}

// optional 把空串转为 nil，使 JSON 序列化为 null。
func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// Bytes 序列化信封，供 SSE 的 data 行复用。
func (e Envelope) Bytes() []byte {
	b, err := json.Marshal(e)
	if err != nil {
		// 信封只含基本类型字段，序列化不会失败
		return nil
	}
	return b
}

// Write 以 status 写回信封并 Abort 后续 handler。Content-Type 固定 application/json。
func Write(rc *app.RequestContext, status int, message, typ, param, code string) {
	rc.SetContentType("application/json")
	rc.SetStatusCode(status)
	rc.Write(New(message, typ, param, code).Bytes())
	rc.Abort()
}
