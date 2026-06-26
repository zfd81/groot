package memory

import "math"

const (
	tokensPerCJKChar = 1.5
	charsPerToken    = 4
)

// TokenEstimator 定义了文本 token 数量估算接口
type TokenEstimator interface {
	// Estimate 估算给定文本的 token 数量
	Estimate(text string) int
}

// DefaultTokenEstimator 默认的 token 估算器实现
// 估算策略:
//   - CJK 字符(中文/日文/韩文): 1.5 tokens/字符
//   - 其他字符(英文/数字/符号): 4 字符/token
type DefaultTokenEstimator struct{}

// Estimate 实现 TokenEstimator 接口
func (e *DefaultTokenEstimator) Estimate(text string) int {
	if text == "" {
		return 0
	}

	cjkCount := 0
	otherCount := 0

	for _, r := range text {
		if isCJK(r) {
			cjkCount++
		} else {
			otherCount++
		}
	}

	// CJK: 1.5 tokens per character
	// Other: 4 characters per token
	cjkTokens := int(math.Round(float64(cjkCount) * tokensPerCJKChar))
	otherTokens := otherCount / charsPerToken

	return cjkTokens + otherTokens
}

// isCJK 判断字符是否为 CJK (中文/日文/韩文) 字符
func isCJK(r rune) bool {
	return (r >= 0x4E00 && r <= 0x9FFF) || // CJK Unified Ideographs
		(r >= 0x3040 && r <= 0x309F) || // Hiragana
		(r >= 0x30A0 && r <= 0x30FF) || // Katakana
		(r >= 0xAC00 && r <= 0xD7AF) // Hangul
}
