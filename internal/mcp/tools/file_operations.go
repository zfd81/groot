package tools

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// FileOperations handles file and directory operations
type FileOperations struct {
	allowedPaths []string
}

// NewFileOperations creates a new file operations handler
func NewFileOperations(allowedPaths []string) *FileOperations {
	return &FileOperations{allowedPaths: allowedPaths}
}

// isPathAllowed checks if path is within allowed directories
// If allowedPaths is empty, all paths are allowed (no restriction)
func (f *FileOperations) isPathAllowed(path string) bool {
	// No restrictions if allowedPaths is empty
	if len(f.allowedPaths) == 0 {
		return true
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		return false
	}

	for _, allowed := range f.allowedPaths {
		absAllowed, err := filepath.Abs(allowed)
		if err != nil {
			continue
		}
		if strings.HasPrefix(absPath, absAllowed) {
			return true
		}
	}
	return false
}

// FileRead reads file content
func (f *FileOperations) FileRead(path string) (string, error) {
	if !f.isPathAllowed(path) {
		return "", fmt.Errorf("path not allowed: %s", path)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("failed to read file: %w", err)
	}
	return string(content), nil
}

// FileWrite writes content to file
func (f *FileOperations) FileWrite(path, content string) error {
	if !f.isPathAllowed(path) {
		return fmt.Errorf("path not allowed: %s", path)
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}
	return nil
}

// FileSearch searches for files matching pattern
func (f *FileOperations) FileSearch(pattern, directory string) ([]string, error) {
	if !f.isPathAllowed(directory) {
		return nil, fmt.Errorf("directory not allowed: %s", directory)
	}

	var matches []string
	err := filepath.WalkDir(directory, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.Contains(d.Name(), pattern) {
			matches = append(matches, path)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("search failed: %w", err)
	}
	return matches, nil
}

// DirectoryList lists directory contents
func (f *FileOperations) DirectoryList(path string) ([]map[string]interface{}, error) {
	if !f.isPathAllowed(path) {
		return nil, fmt.Errorf("path not allowed: %s", path)
	}

	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, fmt.Errorf("failed to list directory: %w", err)
	}

	result := make([]map[string]interface{}, 0, len(entries))
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue
		}
		result = append(result, map[string]interface{}{
			"name":   entry.Name(),
			"type":   entry.Type().String(),
			"size":   info.Size(),
			"is_dir": entry.IsDir(),
		})
	}
	return result, nil
}

// DirectoryCreate creates a directory
func (f *FileOperations) DirectoryCreate(path string) error {
	if !f.isPathAllowed(path) {
		return fmt.Errorf("path not allowed: %s", path)
	}

	if err := os.MkdirAll(path, 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}
	return nil
}

// FileExists checks if file exists
func (f *FileOperations) FileExists(path string) (bool, error) {
	if !f.isPathAllowed(path) {
		return false, fmt.Errorf("path not allowed: %s", path)
	}

	_, err := os.Stat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("failed to check file: %w", err)
	}
	return true, nil
}

// FileInfo returns file information
func (f *FileOperations) FileInfo(path string) (map[string]interface{}, error) {
	if !f.isPathAllowed(path) {
		return nil, fmt.Errorf("path not allowed: %s", path)
	}

	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("failed to get file info: %w", err)
	}

	return map[string]interface{}{
		"name":     info.Name(),
		"size":     info.Size(),
		"mode":     info.Mode().String(),
		"modified": info.ModTime().Format("2006-01-02 15:04:05"),
		"is_dir":   info.IsDir(),
	}, nil
}
