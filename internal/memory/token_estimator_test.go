package memory

import (
	"testing"
)

func TestDefaultTokenEstimator_Estimate_PureEnglish(t *testing.T) {
	estimator := &DefaultTokenEstimator{}
	text := "Hello world"
	expected := 2 // 11 chars / 4 = 2.75 → 2 tokens

	result := estimator.Estimate(text)

	if result != expected {
		t.Errorf("Expected %d tokens for pure English text, got %d", expected, result)
	}
}

func TestDefaultTokenEstimator_Estimate_PureChinese(t *testing.T) {
	estimator := &DefaultTokenEstimator{}
	text := "你好世界"
	expected := 6 // 4 chars * 1.5 = 6 tokens

	result := estimator.Estimate(text)

	if result != expected {
		t.Errorf("Expected %d tokens for pure Chinese text, got %d", expected, result)
	}
}

func TestDefaultTokenEstimator_Estimate_Mixed(t *testing.T) {
	estimator := &DefaultTokenEstimator{}
	text := "Hello 世界"
	// "Hello " = 6 English chars → 6/4 = 1 token
	// "世界" = 2 Chinese chars → 2*1.5 = 3 tokens
	// Total = 1 + 3 = 4 tokens
	expected := 4

	result := estimator.Estimate(text)

	if result != expected {
		t.Errorf("Expected %d tokens for mixed text, got %d", expected, result)
	}
}

func TestDefaultTokenEstimator_Estimate_Empty(t *testing.T) {
	estimator := &DefaultTokenEstimator{}
	text := ""
	expected := 0

	result := estimator.Estimate(text)

	if result != expected {
		t.Errorf("Expected %d tokens for empty string, got %d", expected, result)
	}
}

func TestDefaultTokenEstimator_Estimate_SingleChinese(t *testing.T) {
	estimator := &DefaultTokenEstimator{}
	text := "好" // 1 char * 1.5 = 1.5 → Round 为 2
	expected := 2
	result := estimator.Estimate(text)
	if result != expected {
		t.Errorf("单个中文字符估算错误: got %d, want %d", result, expected)
	}
}

func TestDefaultTokenEstimator_Estimate_ShortEnglish(t *testing.T) {
	estimator := &DefaultTokenEstimator{}
	text := "Hi" // 2 chars / 4 = 0 tokens
	expected := 0
	result := estimator.Estimate(text)
	if result != expected {
		t.Errorf("短英文估算错误: got %d, want %d", result, expected)
	}
}

func TestDefaultTokenEstimator_Estimate_Japanese(t *testing.T) {
	estimator := &DefaultTokenEstimator{}
	text := "こんにちは" // 5 Hiragana chars * 1.5 = 7.5 → Round 为 8
	expected := 8
	result := estimator.Estimate(text)
	if result != expected {
		t.Errorf("日文估算错误: got %d, want %d", result, expected)
	}
}
