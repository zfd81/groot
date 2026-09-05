package logger

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zfd81/groot/internal/config"
)

// LogEntry 结构化的会话日志条目
type LogEntry struct {
	Timestamp string         `json:"timestamp"`
	Level     string         `json:"level"`
	Message   string         `json:"message"`
	Caller    string         `json:"caller"`
	Fields    map[string]any `json:"fields,omitempty"`
}

// standardLogKeys 标准字段集合，其余字段归入 LogEntry.Fields
// 注意与 logger.go getEncoder 中的 EncoderConfig 键名保持一致
var standardLogKeys = map[string]bool{
	"timestamp":  true,
	"level":      true,
	"message":    true,
	"caller":     true,
	"session_id": true,
	"logger":     true,
	"stacktrace": true,
}

// maxLogLineBytes 单条日志行的解析上限（含错误堆栈的长日志也应在此范围内），
// 超过上限的行被丢弃，但不影响同文件后续行的扫描
const maxLogLineBytes = 1024 * 1024

// ReadSessionLogs 扫描最近 days 天的日志文件，返回指定会话最新的至多 limit 条日志。
// 返回值：日志列表（时间正序）、是否因超过 limit 被截断。
// 文件缺失与 JSON 解析失败的行一律跳过，不视为错误。
// days<=0 时不扫描任何文件；limit<=0 表示不限条数。
func ReadSessionLogs(cfg config.LogFileConfig, sessionID string, days, limit int) ([]LogEntry, bool) {
	if sessionID == "" || cfg.Directory == "" || cfg.FilenamePattern == "" {
		return nil, false
	}

	var entries []LogEntry
	truncated := false
	now := time.Now()
	// 从最旧的一天扫到今天，天然保证时间正序
	for i := days - 1; i >= 0; i-- {
		date := now.AddDate(0, 0, -i).Format("2006-01-02")
		filename := strings.Replace(cfg.FilenamePattern, "{date}", date, 1)
		entries = appendSessionLogsFromFile(entries, filepath.Join(cfg.Directory, filename), sessionID)
		// 每处理完一个文件就裁剪到 limit，约束峰值内存；
		// copy 到新切片，避免旧底层大数组因切片引用无法回收
		if limit > 0 && len(entries) > limit {
			entries = append([]LogEntry(nil), entries[len(entries)-limit:]...)
			truncated = true
		}
	}
	return entries, truncated
}

// appendSessionLogsFromFile 逐行读取单个日志文件，追加匹配 sessionID 的条目。
// 超过 maxLogLineBytes 的行仅丢弃自身，扫描继续；最后一行无换行符时同样有效。
func appendSessionLogsFromFile(entries []LogEntry, path, sessionID string) []LogEntry {
	f, err := os.Open(path)
	if err != nil {
		return entries // 文件不存在等：跳过
	}
	defer f.Close()

	reader := bufio.NewReaderSize(f, 64*1024)
	var line []byte
	tooLong := false
	for {
		// ReadLine 从不同时返回数据和错误：无换行符的最后一行会先以
		// isPrefix=false 正常返回，下一次调用才返回 io.EOF
		fragment, isPrefix, err := reader.ReadLine()
		if err != nil {
			break
		}
		if !tooLong {
			if len(line)+len(fragment) > maxLogLineBytes {
				tooLong = true
				line = nil
			} else {
				line = append(line, fragment...)
			}
		}
		if isPrefix {
			continue // 行未结束，继续读取（超长时仅消费不累积）
		}
		if !tooLong {
			entries = appendSessionLogLine(entries, line, sessionID)
		}
		line = line[:0]
		tooLong = false
	}
	return entries
}

// appendSessionLogLine 解析单行 JSON 日志，匹配 sessionID 时追加条目
func appendSessionLogLine(entries []LogEntry, line []byte, sessionID string) []LogEntry {
	var raw map[string]any
	if err := json.Unmarshal(line, &raw); err != nil {
		return entries
	}
	if sid, _ := raw["session_id"].(string); sid != sessionID {
		return entries
	}
	entry := LogEntry{}
	entry.Timestamp, _ = raw["timestamp"].(string)
	entry.Level, _ = raw["level"].(string)
	entry.Message, _ = raw["message"].(string)
	entry.Caller, _ = raw["caller"].(string)
	for k, v := range raw {
		if !standardLogKeys[k] {
			if entry.Fields == nil {
				entry.Fields = map[string]any{}
			}
			entry.Fields[k] = v
		}
	}
	return append(entries, entry)
}
