package logger

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/zfd81/groot/internal/config"
)

// Logger wraps zap.Logger with category support
type Logger struct {
	zap    *zap.Logger
	config config.LoggingConfig
}

// New creates a new logger instance
func New(cfg config.LoggingConfig) *Logger {
	encoder := getEncoder(cfg)

	// Build core with multiple outputs
	cores := []zapcore.Core{}

	// stdout output
	if contains(cfg.Output, "stdout") {
		core := zapcore.NewCore(encoder, zapcore.AddSync(os.Stdout), getLevel(cfg.Level))
		cores = append(cores, core)
	}

	// file output
	if contains(cfg.Output, "file") && cfg.File.Directory != "" {
		fileWriter := getFileWriter(cfg.File)
		core := zapcore.NewCore(encoder, zapcore.AddSync(fileWriter), getLevel(cfg.Level))
		cores = append(cores, core)
	}

	combined := zapcore.NewTee(cores...)

	return &Logger{
		zap:    zap.New(combined, zap.AddCaller()),
		config: cfg,
	}
}

// getEncoder returns JSON encoder
func getEncoder(cfg config.LoggingConfig) zapcore.Encoder {
	encoderConfig := zapcore.EncoderConfig{
		TimeKey:        "timestamp",
		LevelKey:       "level",
		NameKey:        "logger",
		CallerKey:      "caller",
		MessageKey:     "message",
		StacktraceKey:  "stacktrace",
		LineEnding:     zapcore.DefaultLineEnding,
		EncodeLevel:    zapcore.LowercaseLevelEncoder,
		EncodeTime:     zapcore.ISO8601TimeEncoder,
		EncodeDuration: zapcore.SecondsDurationEncoder,
		EncodeCaller:   zapcore.ShortCallerEncoder,
	}

	if cfg.Format == "json" {
		return zapcore.NewJSONEncoder(encoderConfig)
	}
	return zapcore.NewConsoleEncoder(encoderConfig)
}

// getLevel converts string to zap level
func getLevel(level string) zapcore.Level {
	switch strings.ToLower(level) {
	case "debug":
		return zapcore.DebugLevel
	case "info":
		return zapcore.InfoLevel
	case "warn":
		return zapcore.WarnLevel
	case "error":
		return zapcore.ErrorLevel
	default:
		return zapcore.InfoLevel
	}
}

// getFileWriter creates rotating file writer
func getFileWriter(cfg config.LogFileConfig) *rotatingWriter {
	return &rotatingWriter{
		directory:       cfg.Directory,
		filenamePattern: cfg.FilenamePattern,
		maxAge:          cfg.MaxAge, // reserved for future log cleanup
		currentDate:     "",         // force first write to create file
	}
}

// contains checks if string is in slice
func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

// rotatingWriter handles file rotation by date
type rotatingWriter struct {
	directory       string
	filenamePattern string
	maxAge          int // reserved for future log cleanup
	currentDate     string
	currentFile     *os.File
	mu              sync.Mutex
}

// Write writes to current log file
func (w *rotatingWriter) Write(p []byte) (n int, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	today := time.Now().Format("2006-01-02")
	if today != w.currentDate {
		if w.currentFile != nil {
			w.currentFile.Close()
		}
		w.currentDate = today
		filename := strings.Replace(w.filenamePattern, "{date}", today, 1)
		// ensure directory exists
		if err := os.MkdirAll(w.directory, 0755); err != nil {
			return 0, err
		}
		path := filepath.Join(w.directory, filename)
		w.currentFile, err = os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
		if err != nil {
			return 0, err
		}
	}

	return w.currentFile.Write(p)
}

// LogEvent logs a structured event
func (l *Logger) LogEvent(event string, data interface{}) {
	l.zap.Info(event,
		zap.String("event", event),
		zap.Any("data", data),
	)
}

// LogRequest logs API request
func (l *Logger) LogRequest(method, path, taskID, caller string) {
	l.zap.Info("api_request",
		zap.String("event", "api_request"),
		zap.String("method", method),
		zap.String("path", path),
		zap.String("task_id", taskID),
		zap.String("caller", caller),
	)
}

// LogMCPHotReload logs MCP hot-reload event
func (l *Logger) LogMCPHotReload(action, mcpName, mcpType string, count int) {
	l.zap.Info("mcp_hot_reload",
		zap.String("event", "mcp_hot_reload"),
		zap.String("action", action),
		zap.String("mcp_name", mcpName),
		zap.String("mcp_type", mcpType),
		zap.Int("mcp_count", count),
	)
}

// Info logs info message
func (l *Logger) Info(msg string, fields ...zap.Field) {
	l.zap.Info(msg, fields...)
}

// Debug logs debug message
func (l *Logger) Debug(msg string, fields ...zap.Field) {
	l.zap.Debug(msg, fields...)
}

// Warn logs warning message
func (l *Logger) Warn(msg string, fields ...zap.Field) {
	l.zap.Warn(msg, fields...)
}

// Error logs error message
func (l *Logger) Error(msg string, fields ...zap.Field) {
	l.zap.Error(msg, fields...)
}

// NewNop creates a no-op logger for testing
func NewNop() *Logger {
	return &Logger{zap: zap.NewNop()}
}

// Sync flushes any buffered log entries
func (l *Logger) Sync() error {
	return l.zap.Sync()
}
