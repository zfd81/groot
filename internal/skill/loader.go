package skill

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Loader parses SKILL.md files and registers skills
type Loader struct {
	registry *Registry
}

// NewLoader creates a new loader
func NewLoader(registry *Registry) *Loader {
	return &Loader{registry: registry}
}

// LoadAll loads all skills from a directory
func (l *Loader) LoadAll(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // Directory doesn't exist yet
		}
		return err
	}

	for _, entry := range entries {
		// Check if entry is a directory or a symlink pointing to a directory
		isDir := entry.IsDir()
		if entry.Type()&os.ModeSymlink != 0 {
			// For symlinks, check if the target is a directory
			targetPath := filepath.Join(dir, entry.Name())
			info, err := os.Stat(targetPath)
			if err != nil {
				continue // Symlink target doesn't exist or is broken
			}
			isDir = info.IsDir()
		}

		if !isDir {
			continue
		}

		skillPath := filepath.Join(dir, entry.Name(), "SKILL.md")
		if _, err := os.Stat(skillPath); err != nil {
			continue // No SKILL.md in this directory
		}

		if err := l.Load(skillPath); err != nil {
			return fmt.Errorf("failed to load %s: %w", skillPath, err)
		}
	}

	return nil
}

// Load parses a single SKILL.md file
func (l *Loader) Load(path string) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	skill, err := parseSKILLMd(content)
	if err != nil {
		return err
	}

	skill.FilePath = path
	l.registry.Register(skill)

	return nil
}

// parseSKILLMd parses SKILL.md content with YAML frontmatter
func parseSKILLMd(content []byte) (*Skill, error) {
	// Find frontmatter boundaries
	fmStart := bytes.Index(content, []byte("---"))
	if fmStart != 0 {
		return nil, fmt.Errorf("missing frontmatter start")
	}

	fmEnd := bytes.Index(content[3:], []byte("---"))
	if fmEnd < 0 {
		return nil, fmt.Errorf("missing frontmatter end")
	}

	fmContent := content[3 : fmEnd+3]
	mdContent := content[fmEnd+6:]

	// Parse frontmatter YAML
	var skill Skill
	if err := yaml.Unmarshal(fmContent, &skill); err != nil {
		return nil, fmt.Errorf("failed to parse frontmatter: %w", err)
	}

	// Store markdown content as instructions
	skill.Instructions = strings.TrimSpace(string(mdContent))

	// Validate required fields
	if skill.Name == "" {
		return nil, fmt.Errorf("missing required field: name")
	}
	if skill.Description == "" {
		return nil, fmt.Errorf("missing required field: description")
	}

	return &skill, nil
}

// Unload removes a skill by file path
func (l *Loader) Unload(path string) {
	// Find skill by path and remove
	for _, skill := range l.registry.List() {
		if skill.FilePath == path {
			l.registry.Unregister(skill.Name)
			return
		}
	}
}
