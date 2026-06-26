package memory

import (
	"fmt"
	"testing"
	"time"
)

func TestDebugTokenLimitLogic(t *testing.T) {
	mgr := newTestManager(t)
	sessionID := "debug-session"

	if err := mgr.CreateSession(sessionID, "user1"); err != nil {
		t.Fatalf("创建会话失败: %v", err)
	}

	// 添加 3 轮消息
	// 轮1: "AAAA" (1 token per message, 2 total)
	mgr.SaveChatRecord(sessionID, &ChatRecord{
		ChatID:      "20260626100000001",
		SessionID:   sessionID,
		Instruction: "AAAA",
		Result:      "AAAA",
		Status:      "completed",
		StartedAt:   time.Now(),
	})
	// 轮2: "BBBBBBBB" (2 tokens per message, 4 total)
	mgr.SaveChatRecord(sessionID, &ChatRecord{
		ChatID:      "20260626100000002",
		SessionID:   sessionID,
		Instruction: "BBBBBBBB",
		Result:      "BBBBBBBB",
		Status:      "completed",
		StartedAt:   time.Now(),
	})
	// 轮3: "CCCCCCCCCCCCCCCC" (4 tokens per message, 8 total)
	mgr.SaveChatRecord(sessionID, &ChatRecord{
		ChatID:      "20260626100000003",
		SessionID:   sessionID,
		Instruction: "CCCCCCCCCCCCCCCC",
		Result:      "CCCCCCCCCCCCCCCC",
		Status:      "completed",
		StartedAt:   time.Now(),
	})

	// 获取历史并手动执行算法
	history, _ := mgr.GetHistory(sessionID)
	messages := history.Messages

	fmt.Printf("\n=== 消息列表 ===\n")
	estimator := &DefaultTokenEstimator{}
	for i, msg := range messages {
		iTokens := estimator.Estimate(msg.Instruction)
		rTokens := estimator.Estimate(msg.Result)
		total := iTokens + rTokens
		fmt.Printf("Round %d: Instruction=%d, Result=%d, Total=%d\n", msg.Round, iTokens, rTokens, total)
		if i < len(messages)-1 {
			fmt.Printf("  Instruction: %q\n", msg.Instruction)
			fmt.Printf("  Result: %q\n", msg.Result)
		}
	}

	fmt.Printf("\n=== 预算6 tokens的截断过程 ===\n")
	maxContextTokens := 6
	var result []Message
	accumulated := 0

	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		instructionTokens := estimator.Estimate(msg.Instruction)
		resultTokens := estimator.Estimate(msg.Result)
		msgTokens := instructionTokens + resultTokens

		fmt.Printf("检查 Round %d: msgTokens=%d, accumulated=%d, result_len=%d\n", msg.Round, msgTokens, accumulated, len(result))

		if accumulated+msgTokens > maxContextTokens && len(result) > 0 {
			fmt.Printf("  -> 超预算，停止 (%d + %d > %d)\n", accumulated, msgTokens, maxContextTokens)
			break
		}

		fmt.Printf("  -> 加入结果\n")
		result = append([]Message{msg}, result...)
		accumulated += msgTokens
	}

	fmt.Printf("\n最终返回 %d 条消息\n", len(result))
	for _, msg := range result {
		fmt.Printf("  Round %d\n", msg.Round)
	}
}
