package chat

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/zfd81/groot/internal/agent"
	"github.com/zfd81/groot/internal/api/types"
)

// Client communicates with the groot API over HTTP.
type Client struct {
	baseURL   string
	modelName string
	agentName string // 空字符串 == 主 Agent
	sessionID string
	httpCli   *http.Client
}

// NewClient creates a client targeting the given base URL with the default model.
// The HTTP client has no total timeout so SSE streams can run indefinitely;
// only dial and TLS handshake timeouts are set.
func NewClient(baseURL, modelName string) *Client {
	transport := &http.Transport{
		DialContext:       (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
		TLSHandshakeTimeout: 10 * time.Second,
	}
	return &Client{
		baseURL:   strings.TrimRight(baseURL, "/"),
		modelName: modelName,
		httpCli:   &http.Client{Transport: transport},
	}
}

// SetSessionID stores the current session ID.
func (c *Client) SetSessionID(id string) { c.sessionID = id }

// SessionID returns the current session ID.
func (c *Client) SessionID() string { return c.sessionID }

// SetModel updates the model name sent in chat requests.
func (c *Client) SetModel(name string) { c.modelName = name }

// ModelName returns the current model name.
func (c *Client) ModelName() string { return c.modelName }

// SetAgent 设置当前 Agent。空字符串表示主 Agent。
func (c *Client) SetAgent(name string) { c.agentName = name }

// AgentName 返回当前 Agent 名（空 == 主 Agent）。
func (c *Client) AgentName() string { return c.agentName }

// HealthCheck tests whether the service is reachable.
func (c *Client) HealthCheck(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/health", nil)
	if err != nil {
		return err
	}
	resp, err := c.httpCli.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("health returned %d", resp.StatusCode)
	}
	return nil
}

// SendChatStream starts an SSE streaming chat request in a goroutine.
func (c *Client) SendChatStream(instruction string, attachments []types.Attachment, events chan<- tea.Msg, cancelCh <-chan struct{}) {
	go func() {
		defer close(events)

		body := map[string]interface{}{
			"instruction": instruction,
		}
		if len(attachments) > 0 {
			body["attachments"] = attachments
		}
		bodyBytes, _ := json.Marshal(body)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		go func() {
			<-cancelCh
			cancel()
		}()

		req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/chat", bytes.NewReader(bodyBytes))
		if err != nil {
			events <- StreamErrorMsg{Err: err}
			return
		}
		req.Header.Set("Content-Type", "application/json")
		if c.sessionID != "" {
			req.Header.Set("X-Session-ID", c.sessionID)
		}
		req.Header.Set("X-Model-Name", c.modelName)
		// 仅在子 Agent 模式下携带 X-Agent-Name；主 Agent (空串或 MainAgentName) 不发，
		// 减小后端处理负担（chat handler 会显式忽略主 Agent 名）。
		if c.agentName != "" && c.agentName != agent.MainAgentName {
			req.Header.Set("X-Agent-Name", c.agentName)
		}

		resp, err := c.httpCli.Do(req)
		if err != nil {
			events <- StreamErrorMsg{Err: fmt.Errorf("请求失败: %w", err)}
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode >= 400 {
			b, _ := io.ReadAll(resp.Body)
			events <- StreamErrorMsg{Err: fmt.Errorf("API 错误 (%d): %s", resp.StatusCode, string(b))}
			return
		}

		if sid := resp.Header.Get("X-Session-ID"); sid != "" && c.sessionID == "" {
			c.sessionID = sid
			events <- SessionIDMsg(sid)
		}

		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)

		for scanner.Scan() {
			select {
			case <-ctx.Done():
				events <- StreamDoneMsg{}
				return
			default:
			}

			line := scanner.Text()
			if line == "" || strings.HasPrefix(line, ":") {
				continue
			}
			if !strings.HasPrefix(line, "data: ") {
				continue
			}

			data := strings.TrimPrefix(line, "data: ")
			if data == "[DONE]" {
				events <- StreamDoneMsg{}
				return
			}

			var event SseEvent
			if err := json.Unmarshal([]byte(data), &event); err != nil {
				continue
			}
			events <- SseEventMsg(event)
		}

		if err := scanner.Err(); err != nil {
			events <- StreamErrorMsg{Err: err}
			return
		}
		events <- StreamDoneMsg{}
	}()
}

// CancelChat sends a cancel request for the current session.
func (c *Client) CancelChat() error {
	if c.sessionID == "" {
		return nil
	}
	req, err := http.NewRequest("DELETE", c.baseURL+"/chat/"+c.sessionID, nil)
	if err != nil {
		return err
	}
	resp, err := c.httpCli.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// FetchJSON does a GET request and returns the raw bytes.
func (c *Client) FetchJSON(path string) ([]byte, error) {
	resp, err := c.httpCli.Get(c.baseURL + path)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}
