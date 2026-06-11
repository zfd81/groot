package schedule

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// generateExecutionID creates a unique execution ID from the task ID and record metadata.
func generateExecutionID(taskID string, rec *ExecutionRecord) string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		// Fallback: use nanoseconds
		return fmt.Sprintf("%s-%s-%d", taskID, rec.StartedAt.Format("20060102T150405"), rec.StartedAt.UnixNano())
	}
	return fmt.Sprintf("%s-%s-%s", taskID, rec.StartedAt.Format("20060102T150405"), hex.EncodeToString(b))
}
