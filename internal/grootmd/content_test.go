package grootmd

import (
	"sync"
	"testing"
)

func TestSetAndGetContent(t *testing.T) {
	// 清空初始状态
	SetContent("")

	// 设置内容
	SetContent("test content")

	// 获取内容
	got := GetContent()
	if got != "test content" {
		t.Errorf("GetContent() = %s, want test content", got)
	}
}

func TestSetContent_Empty(t *testing.T) {
	SetContent("")

	SetContent("")
	got := GetContent()
	if got != "" {
		t.Errorf("GetContent() after empty SetContent = %s, want empty", got)
	}
}

func TestSetContent_Multiline(t *testing.T) {
	SetContent("")

	multiline := `# GROOT.md

This is a test content.
Multiple lines.

## Section
Content here.
`
	SetContent(multiline)

	got := GetContent()
	if got != multiline {
		t.Errorf("GetContent() for multiline = %s, want %s", got, multiline)
	}
}

func TestSetContent_Overwrite(t *testing.T) {
	SetContent("")

	// 第一次设置
	SetContent("first content")

	// 第二次设置覆盖
	SetContent("second content")

	got := GetContent()
	if got != "second content" {
		t.Errorf("GetContent() after overwrite = %s, want second content", got)
	}
}

func TestGetContent_Concurrent(t *testing.T) {
	SetContent("")

	// 设置初始内容
	SetContent("initial")

	// 并发读取
	var wg sync.WaitGroup
	results := make([]string, 10)

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			results[idx] = GetContent()
		}(i)
	}

	wg.Wait()

	// 所有读取应返回 "initial"
	for i, r := range results {
		if r != "initial" {
			t.Errorf("Concurrent GetContent() at index %d = %s, want initial", i, r)
		}
	}
}

func TestSetContent_Concurrent(t *testing.T) {
	// 并发设置不同内容
	var wg sync.WaitGroup

	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			SetContent("content_" + string(rune('0'+idx)))
		}(i)
	}

	wg.Wait()

	// 最终应该有一个值（可能是任意一个写入的）
	got := GetContent()
	if got == "" {
		t.Error("GetContent() should not be empty after concurrent SetContent")
	}
}

func TestSetGetContent_ConcurrentMix(t *testing.T) {
	SetContent("")

	var wg sync.WaitGroup

	// 5 个写操作
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			SetContent("write_" + string(rune('0'+idx)))
		}(i)
	}

	// 10 个读操作
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			GetContent()
		}()
	}

	wg.Wait()

	// 只验证最终有内容（不验证具体值，因为并发写入顺序不确定）
	got := GetContent()
	if got == "" {
		t.Error("GetContent() should have content after concurrent operations")
	}
}