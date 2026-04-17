package utils

import (
	"fmt"
	"strconv"
	"time"
)

// ParseTime parses yyyyMMddHHmm format to time.Time
func ParseTime(s string) (time.Time, error) {
	if len(s) != 12 {
		return time.Time{}, fmt.Errorf("invalid time format: %s (expected yyyyMMddHHmm)", s)
	}

	year, err := strconv.Atoi(s[0:4])
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid year: %s", s[0:4])
	}

	month, err := strconv.Atoi(s[4:6])
	if err != nil || month < 1 || month > 12 {
		return time.Time{}, fmt.Errorf("invalid month: %s", s[4:6])
	}

	day, err := strconv.Atoi(s[6:8])
	if err != nil || day < 1 || day > 31 {
		return time.Time{}, fmt.Errorf("invalid day: %s", s[6:8])
	}

	hour, err := strconv.Atoi(s[8:10])
	if err != nil || hour < 0 || hour > 23 {
		return time.Time{}, fmt.Errorf("invalid hour: %s", s[8:10])
	}

	minute, err := strconv.Atoi(s[10:12])
	if err != nil || minute < 0 || minute > 59 {
		return time.Time{}, fmt.Errorf("invalid minute: %s", s[10:12])
	}

	return time.Date(year, time.Month(month), day, hour, minute, 0, 0, time.UTC), nil
}