package memory

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestChatRecord_AgentNameSerialization(t *testing.T) {
	r := ChatRecord{
		ChatID:           "chat_x",
		AgentName:        "db-agent",
		PromptTokens:     100,
		CompletionTokens: 50,
		TotalTokens:      150,
	}
	data, err := json.Marshal(&r)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	s := string(data)
	for _, kv := range []string{
		`"agent_name":"db-agent"`,
		`"prompt_tokens":100`,
		`"completion_tokens":50`,
		`"total_tokens":150`,
	} {
		if !strings.Contains(s, kv) {
			t.Errorf("expected %s in JSON, got: %s", kv, s)
		}
	}
}

func TestChatRecord_AgentNameOmitemptyWhenZero(t *testing.T) {
	r := ChatRecord{ChatID: "chat_x"}
	data, _ := json.Marshal(&r)
	s := string(data)
	if strings.Contains(s, `"agent_name"`) {
		t.Errorf("agent_name should be omitted when empty, got: %s", s)
	}
	if strings.Contains(s, `"prompt_tokens"`) {
		t.Errorf("prompt_tokens should be omitted when zero, got: %s", s)
	}
}
