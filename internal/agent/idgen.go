package agent

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

// GenerateTaskID generates a unique task ID
// Format: task-{YYYYMMDD}-{HHMMSSmmm}-{random4}
func GenerateTaskID() string {
	now := time.Now()
	datePart := now.Format("20060102")
	timePart := now.Format("150405.000")
	timePart = timePart[:9] // HHMMSSmmm (remove the dot)

	random := generateRandomHex(4)

	return fmt.Sprintf("task-%s-%s-%s", datePart, timePart, random)
}

// GenerateStepID generates a unique step ID
// Format: {YYYYMMDD}-{HHMMSSmmm}-{random6}
func GenerateStepID() string {
	now := time.Now()
	datePart := now.Format("20060102")
	timePart := now.Format("150405.000")
	timePart = timePart[:9] // HHMMSSmmm (remove the dot)

	random := generateRandomHex(6)

	return fmt.Sprintf("%s-%s-%s", datePart, timePart, random)
}

// generateRandomHex creates random hex string of given length
func generateRandomHex(length int) string {
	bytes := make([]byte, length/2+1)
	_, _ = rand.Read(bytes)
	return hex.EncodeToString(bytes)[:length]
}
