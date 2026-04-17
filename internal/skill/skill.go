package skill

// Skill represents a registered skill
type Skill struct {
	Name         string   `yaml:"name"`
	Description  string   `yaml:"description"`
	Dependencies []string `yaml:"dependencies,omitempty"`
	Instructions string   // Markdown content after frontmatter
	FilePath     string   // Path to SKILL.md
}
