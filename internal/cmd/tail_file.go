package cmd

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
)

// FileWatcher watches log files for changes and handles log rotation
type FileWatcher struct {
	ctx              context.Context
	logDir           string
	currentFile      string
	formatter        *Formatter
	filter           *Filter
	watcher          *fsnotify.Watcher
	currentPosition  int64
}

// NewFileWatcher creates a new FileWatcher instance
func NewFileWatcher(ctx context.Context, logDir, logFile string, formatter *Formatter, filter *Filter) *FileWatcher {
	return &FileWatcher{
		ctx:         ctx,
		logDir:      logDir,
		currentFile: logFile,
		formatter:   formatter,
		filter:      filter,
	}
}

// Start begins watching the log directory for changes
func (fw *FileWatcher) Start() error {
	// Create fsnotify watcher
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("failed to create watcher: %w", err)
	}
	fw.watcher = watcher

	// Watch the log directory (not just file, for rotation handling)
	if err := fw.watcher.Add(fw.logDir); err != nil {
		fw.watcher.Close()
		return fmt.Errorf("failed to watch directory %s: %w", fw.logDir, err)
	}

	// Initialize position to end of file
	if err := fw.initPosition(); err != nil {
		fw.watcher.Close()
		return fmt.Errorf("failed to initialize position: %w", err)
	}

	// Main watching loop
	for {
		select {
		case <-fw.ctx.Done():
			return nil // graceful shutdown

		case event, ok := <-fw.watcher.Events:
			if !ok {
				return nil
			}

			// Handle write event: read new lines from current position
			if event.Op&fsnotify.Write == fsnotify.Write {
				// Only process if it's the current file
				if event.Name == fw.currentFile {
					if err := fw.readNewLines(); err != nil {
						fmt.Fprintf(os.Stderr, "读取新日志行失败: %v\n", err)
					}
				}
			}

			// Handle remove/rename event: find new file, switch to it
			if event.Op&fsnotify.Remove == fsnotify.Remove || event.Op&fsnotify.Rename == fsnotify.Rename {
				// Check if it's the current file being removed/renamed
				if event.Name == fw.currentFile {
					// Find the new log file
					newFile, err := findLatestLogFile(fw.logDir)
					if err != nil {
						fmt.Fprintf(os.Stderr, "查找新日志文件失败: %v\n", err)
						continue
					}

					// Switch to the new file
					fw.currentFile = newFile
					fw.currentPosition = 0
					if err := fw.initPosition(); err != nil {
						fmt.Fprintf(os.Stderr, "Error initializing position for new file: %v\n", err)
					}
				}
			}

		case err, ok := <-fw.watcher.Errors:
			if !ok {
				return nil
			}
			fmt.Fprintf(os.Stderr, "文件监听错误: %v\n", err)
		}
	}
}

// Stop closes the watcher
func (fw *FileWatcher) Stop() {
	if fw.watcher != nil {
		fw.watcher.Close()
	}
}

// initPosition seeks to end of file and stores currentPosition
func (fw *FileWatcher) initPosition() error {
	file, err := os.Open(fw.currentFile)
	if err != nil {
		return fmt.Errorf("failed to open file %s: %w", fw.currentFile, err)
	}
	defer file.Close()

	// Seek to end of file
	pos, err := file.Seek(0, 2)
	if err != nil {
		return fmt.Errorf("failed to seek to end of file: %w", err)
	}

	fw.currentPosition = pos
	return nil
}

// readNewLines reads from current position, formats and prints matching lines, updates position
func (fw *FileWatcher) readNewLines() error {
	file, err := os.Open(fw.currentFile)
	if err != nil {
		return fmt.Errorf("failed to open file %s: %w", fw.currentFile, err)
	}
	defer file.Close()

	// Seek to current position
	_, err = file.Seek(fw.currentPosition, 0)
	if err != nil {
		return fmt.Errorf("failed to seek to position %d: %w", fw.currentPosition, err)
	}

	// Read new lines
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		// Apply filter
		if fw.filter.Match(line) {
			// Format and print
			formatted := fw.formatter.Format(line)
			fmt.Println(formatted)
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("error reading file: %w", err)
	}

	// Update current position
	pos, err := file.Seek(0, 1)
	if err != nil {
		return fmt.Errorf("failed to get current position: %w", err)
	}
	fw.currentPosition = pos

	return nil
}

// findLatestLogFile finds the latest log file in the directory
// Logic:
//  1. Check if directory exists
//  2. Get today's date (YYYY-MM-DD)
//  3. Find files containing today's date in filename
//  4. Sort by modification time, return latest
//  5. Error if no files found: "当天暂无日志文件"
//  6. Error if directory not exists: "日志目录不存在: {dir}"
func findLatestLogFile(logDir string) (string, error) {
	// Check if directory exists
	dirInfo, err := os.Stat(logDir)
	if os.IsNotExist(err) {
		return "", fmt.Errorf("日志目录不存在: %s", logDir)
	}
	if err != nil {
		return "", fmt.Errorf("无法访问日志目录: %w", err)
	}
	if !dirInfo.IsDir() {
		return "", fmt.Errorf("%s 不是目录", logDir)
	}

	// Get today's date (YYYY-MM-DD)
	today := time.Now().Format("2006-01-02")

	// Read directory entries
	entries, err := os.ReadDir(logDir)
	if err != nil {
		return "", fmt.Errorf("无法读取日志目录: %w", err)
	}

	// Find files containing today's date in filename
	var candidates []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.Contains(name, today) {
			candidates = append(candidates, filepath.Join(logDir, name))
		}
	}

	// Error if no files found
	if len(candidates) == 0 {
		return "", fmt.Errorf("当天暂无日志文件")
	}

	// Sort by modification time, return latest
	sort.Slice(candidates, func(i, j int) bool {
		infoI, errI := os.Stat(candidates[i])
		infoJ, errJ := os.Stat(candidates[j])
		if errI != nil || errJ != nil {
			return false
		}
		return infoI.ModTime().After(infoJ.ModTime())
	})

	return candidates[0], nil
}

// readLastNLines reads the last n lines from a file
// If file has fewer than n lines, returns all
func readLastNLines(filePath string, n int) ([]string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open file %s: %w", filePath, err)
	}
	defer file.Close()

	// Read all lines
	var lines []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading file: %w", err)
	}

	// If file has fewer than n lines, return all
	if len(lines) <= n {
		return lines, nil
	}

	// Return last n lines
	return lines[len(lines)-n:], nil
}
