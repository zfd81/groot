package chat

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/zfd81/groot/internal/api/types"
)

var (
	atRefRegex    = regexp.MustCompile(`@(\S+)`)
	barePathRegex = regexp.MustCompile(`(?:^|\s)((?:/|~|\./|\.\./)\S+)`)
)

// ExtractFileRefs extracts all file path references from text.
// Supports both @path (explicit) and bare absolute paths (from drag-and-drop).
// Bare paths are only recognized if the file/directory actually exists.
func ExtractFileRefs(text string) []string {
	var refs []string
	seen := make(map[string]bool)

	// 1. 显式 @path 引用
	for _, m := range atRefRegex.FindAllStringSubmatch(text, -1) {
		path := m[1]
		if !seen[path] {
			seen[path] = true
			refs = append(refs, path)
		}
	}

	// 2. 裸路径（拖拽/粘贴进来的，文件必须存在才识别）
	for _, m := range barePathRegex.FindAllStringSubmatch(text, -1) {
		path := expandTilde(m[1])
		if !seen[path] {
			if _, err := os.Stat(path); err == nil {
				seen[path] = true
				refs = append(refs, path)
			}
		}
	}

	return refs
}

// ReadAttachments reads the referenced files/directories and returns attachments
// along with a mapping from path to file names for text replacement.
func ReadAttachments(paths []string) ([]types.Attachment, map[string][]string, error) {
	var attachments []types.Attachment
	pathToNames := make(map[string][]string)

	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			if os.IsNotExist(err) {
				return nil, nil, fmt.Errorf("路径不存在: %s", path)
			}
			return nil, nil, fmt.Errorf("无法读取: %s", path)
		}

		if info.IsDir() {
			entries, err := os.ReadDir(path)
			if err != nil {
				return nil, nil, fmt.Errorf("无法读取目录: %s", path)
			}
			if len(entries) == 0 {
				return nil, nil, fmt.Errorf("目录为空: %s", path)
			}
			var names []string
			for _, entry := range entries {
				if entry.IsDir() {
					continue
				}
				filePath := filepath.Join(path, entry.Name())
				data, err := os.ReadFile(filePath)
				if err != nil {
					return nil, nil, fmt.Errorf("无法读取文件: %s", filePath)
				}
				attachments = append(attachments, types.Attachment{
					Type:    guessFileType(entry.Name()),
					Name:    entry.Name(),
					Content: base64.StdEncoding.EncodeToString(data),
				})
				names = append(names, entry.Name())
			}
			pathToNames[path] = names
		} else {
			data, err := os.ReadFile(path)
			if err != nil {
				return nil, nil, fmt.Errorf("无法读取文件: %s", path)
			}
			name := filepath.Base(path)
			attachments = append(attachments, types.Attachment{
				Type:    guessFileType(name),
				Name:    name,
				Content: base64.StdEncoding.EncodeToString(data),
			})
			pathToNames[path] = []string{name}
		}
	}

	return attachments, pathToNames, nil
}

// StripFileRefs replaces @path and bare path references with file names.
func StripFileRefs(text string, pathToNames map[string][]string) string {
	result := text
	for path, names := range pathToNames {
		replacement := strings.Join(names, " ")
		// 替换 @path 和 裸路径
		result = strings.ReplaceAll(result, "@"+path, replacement)
		result = strings.ReplaceAll(result, " "+path, " "+replacement)
		result = strings.ReplaceAll(result, "\t"+path, "\t"+replacement)
	}
	return strings.TrimSpace(result)
}

// autoPrefixBarePaths detects bare file paths (from drag-and-drop) that exist on
// disk and inserts @ before them so they are visibly marked as file references.
// Returns the modified text and whether any changes were made.
func autoPrefixBarePaths(text string) (string, bool) {
	matches := barePathRegex.FindAllStringSubmatchIndex(text, -1)
	if len(matches) == 0 {
		return text, false
	}

	result := text
	changed := false
	// Process right-to-left so insertions don't shift earlier match positions
	for i := len(matches) - 1; i >= 0; i-- {
		m := matches[i]
		if len(m) < 4 {
			continue
		}
		pathStart := m[2]
		pathEnd := m[3]
		path := expandTilde(result[pathStart:pathEnd])

		if _, err := os.Stat(path); err != nil {
			continue
		}
		if pathStart > 0 && result[pathStart-1] == '@' {
			continue
		}
		result = result[:pathStart] + "@" + result[pathStart:]
		changed = true
	}
	return result, changed
}

func expandTilde(path string) string {
	if strings.HasPrefix(path, "~") {
		home, err := os.UserHomeDir()
		if err == nil {
			path = home + path[1:]
		}
	}
	return path
}

// extractActiveFileRef extracts the file path after the last @ in text.
func extractActiveFileRef(text string) string {
	atIdx := strings.LastIndex(text, "@")
	if atIdx == -1 {
		return ""
	}
	afterAt := text[atIdx+1:]
	if strings.ContainsAny(afterAt, " \t\n") {
		return ""
	}
	return afterAt
}

// listPathItems lists files/dirs in the directory implied by prefix.
func listPathItems(prefix string) []CompletionItem {
	dir := prefix
	filter := ""

	if !strings.HasSuffix(prefix, "/") {
		dir = filepath.Dir(prefix)
		filter = filepath.Base(prefix)
		if dir == "." || dir == "" {
			dir = "."
		}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	var items []CompletionItem
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() {
			name += "/"
		}
		if filter != "" && !strings.HasPrefix(strings.ToLower(name), strings.ToLower(filter)) {
			continue
		}
		fullPath := filepath.Join(dir, name)
		if strings.HasPrefix(prefix, "/") && !strings.HasPrefix(fullPath, "/") {
			fullPath = "/" + fullPath
		}
		items = append(items, CompletionItem{
			Name:        fullPath,
			Description: "",
		})
	}
	return items
}

func guessFileType(name string) string {
	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".png", ".jpg", ".jpeg", ".gif", ".bmp", ".webp", ".svg":
		return "image"
	case ".mp3", ".wav", ".aac", ".ogg", ".flac":
		return "audio"
	case ".mp4", ".avi", ".mov", ".mkv", ".webm":
		return "video"
	default:
		return "file"
	}
}
