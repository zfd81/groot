package errbody

import (
	"strings"
	"testing"

	"github.com/cloudwego/hertz/pkg/app"
)

func TestNew_NullParamAndCode(t *testing.T) {
	b := New("模型不存在", TypeInvalidRequest, "", "")
	got := string(b.Bytes())
	want := `{"error":{"message":"模型不存在","type":"invalid_request_error","param":null,"code":null}}`
	if got != want {
		t.Fatalf("got %s", got)
	}
}

func TestNew_WithParamAndCode(t *testing.T) {
	b := New("缺少 user", TypeInvalidRequest, "user", CodeInvalidRequest)
	got := string(b.Bytes())
	if !strings.Contains(got, `"param":"user"`) || !strings.Contains(got, `"code":"invalid_request"`) {
		t.Fatalf("got %s", got)
	}
}

func TestWrite(t *testing.T) {
	rc := app.NewContext(0)
	Write(rc, 401, "API Key 无效或缺失", TypeAuthentication, "", CodeInvalidAPIKey)
	if rc.Response.StatusCode() != 401 {
		t.Fatalf("status %d", rc.Response.StatusCode())
	}
	if !strings.Contains(string(rc.Response.Body()), `"type":"authentication_error"`) {
		t.Fatal("body")
	}
	if !rc.IsAborted() {
		t.Fatal("response should be aborted")
	}
}
