package api

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/cloudwego/hertz/pkg/protocol/consts"

	"github.com/zfd81/groot/internal/api/errbody"
)

// assertJSONEnvelope 校验响应是 OpenAI 错误信封：状态码、Content-Type 与四个字段齐全。
func assertJSONEnvelope(t *testing.T, w *ut.ResponseRecorder, status int, code string) {
	t.Helper()
	resp := w.Result()
	if resp.StatusCode() != status {
		t.Fatalf("status = %d, want %d; body=%s", resp.StatusCode(), status, resp.Body())
	}
	if ct := string(resp.Header.ContentType()); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	raw := resp.Body()
	var envl errbody.Envelope
	if err := json.Unmarshal(raw, &envl); err != nil {
		t.Fatalf("响应体不是合法 JSON: %v; body=%s", err, raw)
	}
	// 四字段齐全：message / type / param / code 都必须出现在 JSON 里（无值时是 null）
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		t.Fatalf("解析响应体失败: %v", err)
	}
	inner, ok := top["error"]
	if !ok {
		t.Fatalf("响应体缺少 error 对象: %s", raw)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(inner, &fields); err != nil {
		t.Fatalf("解析 error 对象失败: %v", err)
	}
	for _, k := range []string{"message", "type", "param", "code"} {
		if _, ok := fields[k]; !ok {
			t.Errorf("错误信封缺少字段 %s: %s", k, raw)
		}
	}
	if envl.Error.Type != errbody.TypeInvalidRequest {
		t.Errorf("type = %q, want %q", envl.Error.Type, errbody.TypeInvalidRequest)
	}
	if envl.Error.Code == nil || *envl.Error.Code != code {
		t.Errorf("code = %v, want %s", envl.Error.Code, code)
	}
}

// TestNoRoute_ReturnsJSONEnvelope 未匹配路径返回 404 信封，而不是框架默认的纯文本。
func TestNoRoute_ReturnsJSONEnvelope(t *testing.T) {
	h := server.New()
	registerFallbackHandlers(h)

	w := ut.PerformRequest(h.Engine, consts.MethodGet, "/no/such/path", nil)
	assertJSONEnvelope(t, w, consts.StatusNotFound, errbody.CodeInvalidRequest)
}

// TestNoMethod_ReturnsJSONEnvelope 开启 HandleMethodNotAllowed 时方法不匹配返回 405 信封。
// 生产配置不开启该选项（见 NewServer），这里只验证 NoMethod 兜底本身可用。
func TestNoMethod_ReturnsJSONEnvelope(t *testing.T) {
	h := server.New(server.WithHandleMethodNotAllowed(true))
	registerFallbackHandlers(h)
	h.GET("/only-get", func(ctx context.Context, rc *app.RequestContext) {
		rc.String(consts.StatusOK, "ok")
	})

	w := ut.PerformRequest(h.Engine, consts.MethodPost, "/only-get", nil)
	assertJSONEnvelope(t, w, consts.StatusMethodNotAllowed, errbody.CodeInvalidRequest)
}

// TestMethodMismatch_Returns404 生产配置（未开启 HandleMethodNotAllowed）下，
// 方法不匹配落到 NoRoute 返回 404 信封：该分支只含全局中间件，若改走 405 会绕过
// 挂在路由分组上的鉴权中间件，让无凭证请求探测到路径是否存在。
func TestMethodMismatch_Returns404(t *testing.T) {
	h := server.New()
	registerFallbackHandlers(h)
	h.GET("/only-get", func(ctx context.Context, rc *app.RequestContext) {
		rc.String(consts.StatusOK, "ok")
	})

	w := ut.PerformRequest(h.Engine, consts.MethodPost, "/only-get", nil)
	assertJSONEnvelope(t, w, consts.StatusNotFound, errbody.CodeInvalidRequest)
}
