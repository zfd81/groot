package handler

import (
	"encoding/base64"
	"net/http"
	"testing"

	"github.com/zfd81/groot/internal/api/errbody"
)

// dataURL 按 OpenAI 内容片段的约定拼出 data URL。
func dataURL(mime, payload string) string {
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString([]byte(payload))
}

// requireRequestError 断言解析失败且错误字段与期望一致。
func requireRequestError(t *testing.T, err *requestError, status int, param, code string) {
	t.Helper()
	if err == nil {
		t.Fatalf("期望返回错误，实际解析成功")
	}
	if err.Status != status {
		t.Errorf("status = %d, want %d", err.Status, status)
	}
	if err.Type != errbody.TypeInvalidRequest {
		t.Errorf("type = %q, want %q", err.Type, errbody.TypeInvalidRequest)
	}
	if err.Param != param {
		t.Errorf("param = %q, want %q", err.Param, param)
	}
	if err.Code != code {
		t.Errorf("code = %q, want %q", err.Code, code)
	}
}

func TestParseChatRequest_InvalidMessages(t *testing.T) {
	t.Run("空数组", func(t *testing.T) {
		_, perr := parseChatRequest([]byte(`{"model":"m","messages":[]}`))
		requireRequestError(t, perr, 400, "messages", errbody.CodeInvalidRequest)
	})

	t.Run("末条为assistant", func(t *testing.T) {
		body := `{"messages":[{"role":"user","content":"hi"},{"role":"assistant","content":"hello"}]}`
		_, perr := parseChatRequest([]byte(body))
		requireRequestError(t, perr, 400, "messages", errbody.CodeInvalidRequest)
	})

	t.Run("非法JSON", func(t *testing.T) {
		_, perr := parseChatRequest([]byte(`{not json`))
		requireRequestError(t, perr, http.StatusBadRequest, "", errbody.CodeInvalidRequest)
	})
}

func TestParseChatRequest_UnsupportedRole(t *testing.T) {
	body := `{"messages":[{"role":"user","content":"hi"},{"role":"tool","content":"result"},{"role":"user","content":"继续"}]}`
	_, perr := parseChatRequest([]byte(body))
	requireRequestError(t, perr, 400, "messages", errbody.CodeInvalidRequest)
}

func TestParseChatRequest_NNotOne(t *testing.T) {
	t.Run("n=2", func(t *testing.T) {
		body := `{"messages":[{"role":"user","content":"hi"}],"n":2}`
		_, perr := parseChatRequest([]byte(body))
		requireRequestError(t, perr, 400, "n", errbody.CodeInvalidRequest)
	})

	t.Run("n=1通过", func(t *testing.T) {
		body := `{"messages":[{"role":"user","content":"hi"}],"n":1}`
		if _, perr := parseChatRequest([]byte(body)); perr != nil {
			t.Fatalf("n=1 不应报错，实际 %+v", perr)
		}
	})
}

func TestParseChatRequest_StringContent(t *testing.T) {
	body := `{"model":"qwen-max","messages":[{"role":"user","content":"帮我看下这张发票"}]}`
	got, perr := parseChatRequest([]byte(body))
	if perr != nil {
		t.Fatalf("解析失败：%+v", perr)
	}
	if got.Instruction != "帮我看下这张发票" {
		t.Errorf("Instruction = %q, want %q", got.Instruction, "帮我看下这张发票")
	}
	if len(got.Attachments) != 0 {
		t.Errorf("Attachments 长度 = %d, want 0", len(got.Attachments))
	}
	if got.Model != "qwen-max" {
		t.Errorf("Model = %q, want %q", got.Model, "qwen-max")
	}
	if got.Title != "帮我看下这张发票" {
		t.Errorf("Title = %q, want %q", got.Title, "帮我看下这张发票")
	}
}

func TestParseChatRequest_ImageOnlyContent(t *testing.T) {
	body := `{"messages":[{"role":"user","content":[` +
		`{"type":"image_url","image_url":{"url":"` + dataURL("image/png", "fake-png") + `"}}]}]}`
	got, perr := parseChatRequest([]byte(body))
	if perr != nil {
		t.Fatalf("解析失败：%+v", perr)
	}
	if got.Instruction != "" {
		t.Errorf("Instruction = %q, want 空串", got.Instruction)
	}
	if len(got.Attachments) != 1 {
		t.Fatalf("Attachments 长度 = %d, want 1", len(got.Attachments))
	}
	att := got.Attachments[0]
	if att.Name != "image.png" {
		t.Errorf("Name = %q, want %q", att.Name, "image.png")
	}
	if att.Type != "image" {
		t.Errorf("Type = %q, want %q", att.Type, "image")
	}
	if att.MIMEType != "image/png" {
		t.Errorf("MIMEType = %q, want %q", att.MIMEType, "image/png")
	}
	if att.Base64Data != base64.StdEncoding.EncodeToString([]byte("fake-png")) {
		t.Errorf("Base64Data 未保留原始 payload")
	}
	// 图片之外的文本为空时，标题取兜底文案
	if got.Title != "新会话" {
		t.Errorf("Title = %q, want %q", got.Title, "新会话")
	}
}

// 末条 user 的 content 只接受字符串、片段数组，以及字段缺失/显式 null。
// 其它类型（对象、数字、布尔）说明客户端少写了一层数组或写错了字段，
// 静默当空串会让后续 handler 正常跑一轮却什么都不带，调用方拿不到任何错误。
func TestParseChatRequest_RejectsNonStringNonArrayUserContent(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{
			name: "对象少写一层数组",
			body: `{"messages":[{"role":"user","content":{"type":"image_url","image_url":{"url":"` +
				dataURL("image/png", "fake-png") + `"}}}]}`,
		},
		{name: "数字", body: `{"messages":[{"role":"user","content":42}]}`},
		{name: "布尔", body: `{"messages":[{"role":"user","content":true}]}`},
		{name: "content缺失", body: `{"messages":[{"role":"user"}]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, perr := parseChatRequest([]byte(tc.body))
			requireRequestError(t, perr, 400, "messages", errbody.CodeInvalidRequest)
		})
	}
}

// 显式 null 与「只有片段的数组」是合法输入，修复类型校验后不得被误伤。
func TestParseChatRequest_AllowsNullUserContent(t *testing.T) {
	got, perr := parseChatRequest([]byte(`{"messages":[{"role":"user","content":null}]}`))
	if perr != nil {
		t.Fatalf("content 为 null 不应报错：%+v", perr)
	}
	if got.Instruction != "" || len(got.Attachments) != 0 {
		t.Errorf("Instruction/Attachments = %q/%d, want 空串/0", got.Instruction, len(got.Attachments))
	}
}

func TestParseChatRequest_VideoRejected(t *testing.T) {
	body := `{"messages":[{"role":"user","content":[` +
		`{"type":"video_url","video_url":{"url":"` + dataURL("video/mp4", "fake") + `"}}]}]}`
	got, perr := parseChatRequest([]byte(body))
	if got != nil {
		t.Errorf("期望返回 nil 结果，实际 %+v", got)
	}
	requireRequestError(t, perr, 400, "messages", errbody.CodeUnsupportedType)
}

func TestParseChatRequest_FileTextOrRejected(t *testing.T) {
	t.Run("text/plain通过", func(t *testing.T) {
		body := `{"messages":[{"role":"user","content":[` +
			`{"type":"text","text":"总结这个文件"},` +
			`{"type":"file","file":{"filename":"a.txt","file_data":"` + dataURL("text/plain", "hello") + `"}}]}]}`
		got, perr := parseChatRequest([]byte(body))
		if perr != nil {
			t.Fatalf("解析失败：%+v", perr)
		}
		if got.Instruction != "总结这个文件" {
			t.Errorf("Instruction = %q, want %q", got.Instruction, "总结这个文件")
		}
		if len(got.Attachments) != 1 {
			t.Fatalf("Attachments 长度 = %d, want 1", len(got.Attachments))
		}
		att := got.Attachments[0]
		if att.Type != "file" || att.Name != "a.txt" {
			t.Errorf("附件 = %+v, want Type=file Name=a.txt", att)
		}
		if att.DecodedContent != "hello" {
			t.Errorf("DecodedContent = %q, want %q", att.DecodedContent, "hello")
		}
		if att.MIMEType != "text/plain" {
			t.Errorf("MIMEType = %q, want %q", att.MIMEType, "text/plain")
		}
	})

	t.Run("application/pdf拒绝", func(t *testing.T) {
		body := `{"messages":[{"role":"user","content":[` +
			`{"type":"file","file":{"filename":"a.pdf","file_data":"` + dataURL("application/pdf", "%PDF") + `"}}]}]}`
		_, perr := parseChatRequest([]byte(body))
		requireRequestError(t, perr, 400, "messages", errbody.CodeUnsupportedFileType)
	})
}

func TestParseChatRequest_BadBase64(t *testing.T) {
	body := `{"messages":[{"role":"user","content":[` +
		`{"type":"image_url","image_url":{"url":"data:image/png;base64,!!!!"}}]}]}`
	_, perr := parseChatRequest([]byte(body))
	requireRequestError(t, perr, 400, "messages", errbody.CodeInvalidRequest)
}

// file 片段的 file_data 解不出 Base64 时同样归为请求本身不合法，
// 对外 code 统一为 invalid_request，细分原因只出现在 message 里。
func TestParseChatRequest_BadBase64InFilePart(t *testing.T) {
	body := `{"messages":[{"role":"user","content":[` +
		`{"type":"file","file":{"filename":"a.txt","file_data":"data:text/plain;base64,invalid!!!base64"}}]}]}`
	_, perr := parseChatRequest([]byte(body))
	requireRequestError(t, perr, 400, "messages", errbody.CodeInvalidRequest)
}

// data URL 的 MIME 可能携带 charset 等参数：只取第一个 ; 之前的部分，
// 否则 text/csv;charset=utf-8 会被误判为非文本类型。
func TestParseChatRequest_DataURLCharsetStripped(t *testing.T) {
	body := `{"messages":[{"role":"user","content":[` +
		`{"type":"file","file":{"filename":"a.csv","file_data":"data:text/csv;charset=utf-8;base64,` +
		base64.StdEncoding.EncodeToString([]byte("a,b\n1,2")) + `"}}]}]}`
	got, perr := parseChatRequest([]byte(body))
	if perr != nil {
		t.Fatalf("带 charset 的 data URL 不应报错：%+v", perr)
	}
	if len(got.Attachments) != 1 {
		t.Fatalf("Attachments 长度 = %d, want 1", len(got.Attachments))
	}
	if att := got.Attachments[0]; att.MIMEType != "text/csv" {
		t.Errorf("MIMEType = %q, want %q", att.MIMEType, "text/csv")
	}
}

// 空 MIME（data:;base64,...）保持既有语义：解析成功但 MIME 为空，
// 随后按非文本类型拒绝，带 charset 的剥离逻辑不改变这一点。
func TestParseChatRequest_EmptyMIMEKeepsRejected(t *testing.T) {
	body := `{"messages":[{"role":"user","content":[` +
		`{"type":"file","file":{"filename":"a.txt","file_data":"data:;base64,` +
		base64.StdEncoding.EncodeToString([]byte("hello")) + `"}}]}]}`
	_, perr := parseChatRequest([]byte(body))
	requireRequestError(t, perr, 400, "messages", errbody.CodeUnsupportedFileType)
}

func TestParseChatRequest_IncludeUsageIgnoredWhenNotStream(t *testing.T) {
	body := `{"messages":[{"role":"user","content":"hi"}],"stream":false,"stream_options":{"include_usage":true}}`
	got, perr := parseChatRequest([]byte(body))
	if perr != nil {
		t.Fatalf("stream=false 时 include_usage 不应报错：%+v", perr)
	}
	if got.IncludeUsage {
		t.Errorf("IncludeUsage = true, want false")
	}
	if got.Stream {
		t.Errorf("Stream = true, want false")
	}

	streamBody := `{"messages":[{"role":"user","content":"hi"}],"stream":true,"stream_options":{"include_usage":true}}`
	got, perr = parseChatRequest([]byte(streamBody))
	if perr != nil {
		t.Fatalf("解析失败：%+v", perr)
	}
	if !got.Stream || !got.IncludeUsage {
		t.Errorf("Stream/IncludeUsage = %v/%v, want true/true", got.Stream, got.IncludeUsage)
	}
}

func TestParseChatRequest_SystemAndHistory(t *testing.T) {
	body := `{"messages":[` +
		`{"role":"system","content":"你是财务助手"},` +
		`{"role":"system","content":"回答要简洁"},` +
		`{"role":"user","content":"第一个问题"},` +
		`{"role":"assistant","content":"第一个回答"},` +
		`{"role":"user","content":"第二个问题"}]}`
	got, perr := parseChatRequest([]byte(body))
	if perr != nil {
		t.Fatalf("解析失败：%+v", perr)
	}
	if want := "你是财务助手\n\n回答要简洁"; got.SystemPrompt != want {
		t.Errorf("SystemPrompt = %q, want %q", got.SystemPrompt, want)
	}
	if got.Instruction != "第二个问题" {
		t.Errorf("Instruction = %q, want %q", got.Instruction, "第二个问题")
	}
	if len(got.CallerHistory) != 2 {
		t.Fatalf("CallerHistory 长度 = %d, want 2", len(got.CallerHistory))
	}
	if got.CallerHistory[0].Instruction != "第一个问题" {
		t.Errorf("CallerHistory[0].Instruction = %q", got.CallerHistory[0].Instruction)
	}
	if got.CallerHistory[1].Result != "第一个回答" {
		t.Errorf("CallerHistory[1].Result = %q", got.CallerHistory[1].Result)
	}
}

// TestParseChatRequest_NoHistory 验证 no_history 开关的解析：缺省与 false 都表示
// 沿用现有行为（带服务端历史），只有显式 true 才关闭历史上下文。
func TestParseChatRequest_NoHistory(t *testing.T) {
	cases := []struct {
		name string
		body string
		want bool
	}{
		{"缺省", `{"messages":[{"role":"user","content":"hi"}]}`, false},
		{"显式false", `{"messages":[{"role":"user","content":"hi"}],"no_history":false}`, false},
		{"显式true", `{"messages":[{"role":"user","content":"hi"}],"no_history":true}`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, perr := parseChatRequest([]byte(tc.body))
			if perr != nil {
				t.Fatalf("期望解析成功，实际错误: %+v", perr)
			}
			if p.NoHistory != tc.want {
				t.Errorf("NoHistory = %v, want %v", p.NoHistory, tc.want)
			}
		})
	}
}
