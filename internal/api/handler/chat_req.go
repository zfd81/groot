package handler

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/zfd81/groot/internal/agent"
	"github.com/zfd81/groot/internal/api/errbody"
	"github.com/zfd81/groot/internal/memory"
)

// fallbackTitle 是末条 user 只有内容片段（图片/音频）、没有文本时的会话标题。
const fallbackTitle = "新会话"

// chatRequest 是 POST /chat/completions 的请求体。采样参数接受但忽略：
// 采样设置属于模型部署配置，同一模型被多方复用时由模型记录统一约束。
type chatRequest struct {
	Model               string          `json:"model"`
	Messages            []chatMessage   `json:"messages"`
	Stream              bool            `json:"stream"`
	StreamOptions       *streamOptions  `json:"stream_options"`
	SessionID           string          `json:"session_id"`
	AgentName           string          `json:"agent_name"`
	User                string          `json:"user"`
	N                   *int            `json:"n"`
	Temperature         *float64        `json:"temperature"`
	TopP                *float64        `json:"top_p"`
	MaxTokens           *int            `json:"max_tokens"`
	MaxCompletionTokens *int            `json:"max_completion_tokens"`
	Stop                json.RawMessage `json:"stop"`
	PresencePenalty     *float64        `json:"presence_penalty"`
	FrequencyPenalty    *float64        `json:"frequency_penalty"`
	Seed                *int            `json:"seed"`
	ResponseFormat      json.RawMessage `json:"response_format"`
	Tools               json.RawMessage `json:"tools"`
	ToolChoice          json.RawMessage `json:"tool_choice"`
	Logprobs            *bool           `json:"logprobs"`
	TopLogprobs         *int            `json:"top_logprobs"`
}

type chatMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"` // 字符串或内容片段数组
}

type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

// contentPart 是 user 消息 content 数组的元素，只声明本接口会用到的字段；
// 其余字段（如 image_url.detail）由 encoding/json 忽略，符合「接受但不生效」。
type contentPart struct {
	Type     string `json:"type"`
	Text     string `json:"text"`
	ImageURL *struct {
		URL string `json:"url"`
	} `json:"image_url"`
	InputAudio *struct {
		Data   string `json:"data"`
		Format string `json:"format"`
	} `json:"input_audio"`
	File *struct {
		Filename string `json:"filename"`
		FileData string `json:"file_data"`
	} `json:"file"`
}

// parsedRequest 是解析结果，供 handler 组装 agent.Task。
type parsedRequest struct {
	Instruction   string // 末条 user 的文本（可能为空串）
	Attachments   []agent.MultimodalContent
	SystemPrompt  string           // 全部 system 消息按出现顺序空行连接
	CallerHistory []memory.Message // 除 system 与末条 user 之外的全部消息
	Title         string           // 会话标题素材：Instruction 前若干字符，为空时取 "新会话"
	Model         string
	SessionID     string
	AgentName     string
	User          string
	Stream        bool
	IncludeUsage  bool
}

// requestError 承载校验失败时的 OpenAI 信封字段，handler 直接转成响应。
type requestError struct {
	Status  int
	Message string
	Type    string
	Param   string
	Code    string
}

// newRequestError 构造 400 校验错误；信封 type 默认 invalid_request_error。
func newRequestError(message, param, code string) *requestError {
	return &requestError{
		Status:  http.StatusBadRequest,
		Message: message,
		Type:    errbody.TypeInvalidRequest,
		Param:   param,
		Code:    code,
	}
}

// parseChatRequest 按 spec §1.4.7 的 1~7 步校验并解析请求体。
// 附件数量、体积、类型白名单（第 8 步）依赖配置，由 handler 在拿到 Attachments 后处理。
func parseChatRequest(body []byte) (*parsedRequest, *requestError) {
	var req chatRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, newRequestError("请求体不是合法的 JSON："+err.Error(), "", errbody.CodeInvalidRequest)
	}

	if len(req.Messages) == 0 {
		return nil, newRequestError("messages 不能为空", "messages", errbody.CodeInvalidRequest)
	}

	// 先整体校验消息结构，保证「格式问题」先于「内容片段问题」报出。
	for _, msg := range req.Messages {
		switch msg.Role {
		case "system", "user", "assistant":
		default:
			return nil, newRequestError("不支持的消息角色："+msg.Role+"（仅支持 system、user、assistant）", "messages", errbody.CodeInvalidRequest)
		}
		// 只有 user 允许 content 为内容片段数组，其余角色必须是字符串，
		// 以免把数组语义扩展到本接口不支持的消息类型上。
		if msg.Role != "user" && !isJSONString(msg.Content) && !isJSONNullOrEmpty(msg.Content) {
			return nil, newRequestError(msg.Role+" 消息的 content 必须是字符串", "messages", errbody.CodeInvalidRequest)
		}
	}

	last := req.Messages[len(req.Messages)-1]
	if last.Role != "user" {
		return nil, newRequestError("最后一条消息的 role 必须是 user", "messages", errbody.CodeInvalidRequest)
	}

	// OpenAI 自身要求 n >= 1，本接口只实现单候选，因此除缺省外只接受 1。
	if req.N != nil && *req.N != 1 {
		return nil, newRequestError("n 仅支持取 1", "n", errbody.CodeInvalidRequest)
	}

	out := &parsedRequest{
		Model:     req.Model,
		SessionID: req.SessionID,
		AgentName: req.AgentName,
		User:      req.User,
		Stream:    req.Stream,
		// stream=false 时 include_usage 无意义，按「接受但忽略」处理。
		IncludeUsage: req.Stream && req.StreamOptions != nil && req.StreamOptions.IncludeUsage,
	}

	var systemParts []string
	history := make([]memory.Message, 0, len(req.Messages))
	for i, msg := range req.Messages {
		switch msg.Role {
		case "system":
			s := decodeStringContent(msg.Content)
			if s != "" {
				systemParts = append(systemParts, s)
			}
		case "user":
			if i == len(req.Messages)-1 {
				text, atts, rerr := parseUserContent(msg.Content)
				if rerr != nil {
					return nil, rerr
				}
				out.Instruction = text
				out.Attachments = atts
				continue
			}
			// 非末条 user 也允许内容片段数组，只取其文本进历史：
			// memory.Message 只承载文本，历史里的图片/音频无处安放。
			text, _, rerr := parseUserContent(msg.Content)
			if rerr != nil {
				return nil, rerr
			}
			history = append(history, memory.Message{Instruction: text})
		case "assistant":
			history = append(history, memory.Message{Result: decodeStringContent(msg.Content)})
		}
	}
	out.SystemPrompt = strings.Join(systemParts, "\n\n")
	history = dropEmptyMessages(history)
	out.CallerHistory = history

	out.Title = memory.TruncateTitle(out.Instruction)
	if out.Title == "" {
		out.Title = fallbackTitle
	}
	return out, nil
}

// dropEmptyMessages 丢掉 content 为空的占位消息：空 Instruction 与空 Result
// 在引擎里会被直接跳过，保留它们只会让 history 数组带进无用元素。
func dropEmptyMessages(msgs []memory.Message) []memory.Message {
	out := make([]memory.Message, 0, len(msgs))
	for _, m := range msgs {
		if m.Instruction == "" && m.Result == "" {
			continue
		}
		out = append(out, m)
	}
	return out
}

// parseUserContent 把末条 user 的 content 拆成文本与多模态附件。
// 合法输入只有字符串、内容片段数组，以及显式 null（只有图片/音频的消息是
// 合法场景）；字段缺失或其它类型一律拒绝：把对象/数字静默当空串会让
// 「少写一层数组」的请求带着空指令正常执行一轮，调用方既拿不到错误，
// 也拿不到自己预期的图片。
func parseUserContent(raw json.RawMessage) (string, []agent.MultimodalContent, *requestError) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return "", nil, newRequestError("user 消息缺少 content 字段", "messages", errbody.CodeInvalidRequest)
	}
	if bytes.Equal(trimmed, []byte("null")) {
		return "", nil, nil
	}
	if isJSONString(trimmed) {
		return decodeStringContent(raw), nil, nil
	}
	if trimmed[0] != '[' {
		return "", nil, newRequestError("user 消息的 content 必须是字符串或内容片段数组", "messages", errbody.CodeInvalidRequest)
	}

	var parts []contentPart
	if err := json.Unmarshal(trimmed, &parts); err != nil {
		return "", nil, newRequestError("messages[].content 不是合法的内容片段数组："+err.Error(), "messages", errbody.CodeInvalidRequest)
	}

	var text strings.Builder
	var atts []agent.MultimodalContent
	for _, p := range parts {
		switch p.Type {
		case "text":
			// 多个 text 片段按出现顺序拼接，不额外插入分隔符，保持原文连续性。
			text.WriteString(p.Text)
		case "image_url":
			att, rerr := parseImagePart(p)
			if rerr != nil {
				return "", nil, rerr
			}
			atts = append(atts, att)
		case "input_audio":
			att, rerr := parseAudioPart(p)
			if rerr != nil {
				return "", nil, rerr
			}
			atts = append(atts, att)
		case "file":
			att, rerr := parseFilePart(p)
			if rerr != nil {
				return "", nil, rerr
			}
			atts = append(atts, att)
		case "video_url":
			return "", nil, newRequestError("不支持的内容片段类型：video_url", "messages", errbody.CodeUnsupportedType)
		default:
			return "", nil, newRequestError("不支持的内容片段类型："+p.Type, "messages", errbody.CodeUnsupportedType)
		}
	}
	return text.String(), atts, nil
}

func parseImagePart(p contentPart) (agent.MultimodalContent, *requestError) {
	if p.ImageURL == nil || p.ImageURL.URL == "" {
		return agent.MultimodalContent{}, newRequestError("image_url 片段缺少 image_url.url", "messages", errbody.CodeInvalidRequest)
	}
	mime, payload, _, err := decodeDataURL(p.ImageURL.URL)
	if err != nil {
		return agent.MultimodalContent{}, decodeError("image_url", err)
	}
	return agent.MultimodalContent{
		Type:       "image",
		Name:       synthesizeImageName(mime),
		MIMEType:   mime,
		Base64Data: payload,
	}, nil
}

func parseAudioPart(p contentPart) (agent.MultimodalContent, *requestError) {
	if p.InputAudio == nil || p.InputAudio.Data == "" {
		return agent.MultimodalContent{}, newRequestError("input_audio 片段缺少 input_audio.data", "messages", errbody.CodeInvalidRequest)
	}
	format := strings.ToLower(strings.TrimSpace(p.InputAudio.Format))
	var name, mime string
	switch format {
	case "wav":
		name, mime = "audio.wav", "audio/wav"
	case "mp3":
		name, mime = "audio.mp3", "audio/mpeg"
	default:
		return agent.MultimodalContent{}, newRequestError("input_audio.format 仅支持 wav、mp3，实际为："+p.InputAudio.Format, "messages", errbody.CodeInvalidRequest)
	}
	if _, err := decodeBase64(p.InputAudio.Data); err != nil {
		return agent.MultimodalContent{}, decodeError("input_audio", err)
	}
	return agent.MultimodalContent{
		Type:       "audio",
		Name:       name,
		MIMEType:   mime,
		Base64Data: p.InputAudio.Data,
	}, nil
}

func parseFilePart(p contentPart) (agent.MultimodalContent, *requestError) {
	if p.File == nil || p.File.FileData == "" {
		return agent.MultimodalContent{}, newRequestError("file 片段缺少 file.file_data", "messages", errbody.CodeInvalidRequest)
	}
	mime, payload, decoded, err := decodeDataURL(p.File.FileData)
	if err != nil {
		return agent.MultimodalContent{}, decodeError("file", err)
	}
	if !isTextMIME(mime) {
		return agent.MultimodalContent{}, newRequestError("file 片段只接受文本类内容（text/*、application/json、application/xml、*+json、*+xml），实际为："+mime, "messages", errbody.CodeUnsupportedFileType)
	}
	name := p.File.Filename
	if name == "" {
		name = synthesizeFileName(mime)
	}
	return agent.MultimodalContent{
		Type:           "file",
		Name:           name,
		MIMEType:       mime,
		Base64Data:     payload,
		DecodedContent: string(decoded),
	}, nil
}

// decodeError 处理 data URL / base64 解析失败：Base64 解码失败属于请求本身不合法，
// 对外统一为 invalid_request，细分原因只出现在 message 里。
func decodeError(part string, err error) *requestError {
	return newRequestError(part+" 片段的内容无法按 Base64 解码："+err.Error(), "messages", errbody.CodeInvalidRequest)
}

// decodeDataURL 拆解 data:<mime>;base64,<payload>，返回 MIME、原始 payload 与解码字节。
func decodeDataURL(raw string) (string, string, []byte, error) {
	header, payload, ok := strings.Cut(raw, ",")
	if !ok || !strings.HasPrefix(header, "data:") {
		return "", "", nil, errInvalidDataURL
	}
	meta := strings.TrimPrefix(header, "data:")
	const base64Suffix = ";base64"
	if !strings.HasSuffix(meta, base64Suffix) {
		return "", "", nil, errInvalidDataURL
	}
	mime := strings.TrimSpace(strings.TrimSuffix(meta, base64Suffix))
	// 真实客户端常带 charset 等参数（如 text/csv;charset=utf-8;base64），
	// MIME 只取第一个 ; 之前的部分，参数不参与类型判定。
	if i := strings.Index(mime, ";"); i >= 0 {
		mime = strings.TrimSpace(mime[:i])
	}
	decoded, err := decodeBase64(payload)
	if err != nil {
		return "", "", nil, err
	}
	return strings.ToLower(mime), payload, decoded, nil
}

type dataURLError struct{}

func (dataURLError) Error() string { return "不是 data:<mime>;base64,<payload> 形式" }

var errInvalidDataURL = dataURLError{}

// decodeBase64 依次尝试标准、无填充、URL 安全三种字母表：
// 不同客户端导出的 Base64 在填充与字符集上并不统一，收紧会误伤合法请求。
func decodeBase64(s string) ([]byte, error) {
	encodings := []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	}
	var lastErr error
	for _, enc := range encodings {
		b, err := enc.DecodeString(s)
		if err == nil {
			return b, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

// isTextMIME 判断 MIME 是否属于可当作文本读取的类型。
func isTextMIME(mime string) bool {
	m := strings.ToLower(strings.TrimSpace(mime))
	if strings.HasPrefix(m, "text/") {
		return true
	}
	switch m {
	case "application/json", "application/xml":
		return true
	}
	return strings.HasSuffix(m, "+json") || strings.HasSuffix(m, "+xml")
}

// synthesizeImageName 由 MIME 合成图片附件名：附件校验按扩展名做白名单，
// 而 OpenAI 的图片片段不带文件名。
func synthesizeImageName(mime string) string {
	switch mime {
	case "image/jpeg", "image/jpg":
		return "image.jpg"
	case "image/webp":
		return "image.webp"
	case "image/gif":
		return "image.gif"
	case "image/bmp":
		return "image.bmp"
	default:
		return "image.png"
	}
}

// synthesizeFileName 在 file 片段缺少 filename 时由 MIME 推导文件名，
// 推导不出的文本类一律落到 file.txt。
func synthesizeFileName(mime string) string {
	switch mime {
	case "text/plain":
		return "file.txt"
	case "text/csv":
		return "file.csv"
	case "text/markdown":
		return "file.md"
	case "text/html":
		return "file.html"
	case "application/json":
		return "file.json"
	case "application/xml":
		return "file.xml"
	}
	if strings.HasSuffix(mime, "+json") {
		return "file.json"
	}
	if strings.HasSuffix(mime, "+xml") {
		return "file.xml"
	}
	return "file.txt"
}

// isJSONString 判断原始 JSON 是否以字符串形式给出。
func isJSONString(raw json.RawMessage) bool {
	t := bytes.TrimSpace(raw)
	return len(t) > 0 && t[0] == '"'
}

// isJSONNullOrEmpty 判断字段缺失或显式 null，这两种情况按空字符串处理。
func isJSONNullOrEmpty(raw json.RawMessage) bool {
	t := bytes.TrimSpace(raw)
	return len(t) == 0 || bytes.Equal(t, []byte("null"))
}

// decodeStringContent 把字符串形式的 content 解出；字段缺失、null、
// 非字符串一律返回空串，非字符串的合法性已在消息结构校验阶段拦截。
func decodeStringContent(raw json.RawMessage) string {
	if !isJSONString(raw) {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return ""
	}
	return s
}
