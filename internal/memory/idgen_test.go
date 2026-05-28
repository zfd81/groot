package memory

import (
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestGenerateChildChatID_Format(t *testing.T) {
	parent := "chat_20260524103000523"
	got := GenerateChildChatID(parent, "db-agent")

	// 前缀必须是父 chatID + "_"
	if !strings.HasPrefix(got, parent+"_") {
		t.Fatalf("child chatID must start with parent+'_': got %q", got)
	}
	// 整体格式: chat_{14digits3}_{9digits}_{4lowerAlnum}_{agentName}
	re := regexp.MustCompile(`^chat_\d{17}_\d{9}_[a-z0-9]{4}_db-agent$`)
	if !re.MatchString(got) {
		t.Fatalf("child chatID format mismatch: %q", got)
	}
}

func TestGenerateChildChatID_UniqueWithinMillisecond(t *testing.T) {
	parent := "chat_20260524103000523"
	seen := make(map[string]struct{}, 1000)
	deadline := time.Now().Add(50 * time.Millisecond)
	for time.Now().Before(deadline) {
		id := GenerateChildChatID(parent, "x")
		if _, dup := seen[id]; dup {
			t.Fatalf("duplicate child chatID generated: %s", id)
		}
		seen[id] = struct{}{}
	}
	if len(seen) < 100 {
		t.Fatalf("too few samples generated (%d), test environment too slow?", len(seen))
	}
}

func TestGenerateChildChatID_ConcurrentNoDuplicates(t *testing.T) {
	const goroutines = 32
	const perGoroutine = 200
	parent := "chat_20260524103000523"

	type result struct {
		ids []string
	}
	results := make(chan result, goroutines)

	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ids := make([]string, perGoroutine)
			for i := 0; i < perGoroutine; i++ {
				ids[i] = GenerateChildChatID(parent, "x")
			}
			results <- result{ids: ids}
		}()
	}
	wg.Wait()
	close(results)

	seen := make(map[string]struct{}, goroutines*perGoroutine)
	for r := range results {
		for _, id := range r.ids {
			if _, dup := seen[id]; dup {
				t.Fatalf("duplicate child chatID under concurrency: %s", id)
			}
			seen[id] = struct{}{}
		}
	}
	if len(seen) != goroutines*perGoroutine {
		t.Fatalf("expected %d unique IDs, got %d", goroutines*perGoroutine, len(seen))
	}
}
