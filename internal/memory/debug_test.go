package memory

import (
	"fmt"
	"testing"
)

func TestDebugTokenEstimate(t *testing.T) {
	e := &DefaultTokenEstimator{}

	tests := []string{"A", "BBBB", "CCCCCCCC", "AAAA"}
	for _, text := range tests {
		tokens := e.Estimate(text)
		fmt.Printf("%q: %d tokens (len=%d)\n", text, tokens, len(text))
	}
}
