package filesystem

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	einoskill "github.com/cloudwego/eino/adk/middlewares/skill"
	"github.com/cloudwego/eino-ext/adk/backend/local"

	"github.com/zfd81/groot/internal/cmd"
)

func TestSymlinkBackend_LoadRealSkills(t *testing.T) {
	skillsDir := filepath.Join(cmd.GetDefaultHome(), "skills")
	if _, err := os.Stat(skillsDir); os.IsNotExist(err) {
		t.Skip("Skills dir not found:", skillsDir)
	}

	localBackend, err := local.NewBackend(context.Background(), &local.Config{})
	if err != nil {
		t.Fatal("Failed to create local backend:", err)
	}

	symlinkBackend := NewSymlinkBackend(localBackend)
	backend, err := einoskill.NewBackendFromFilesystem(context.Background(), &einoskill.BackendFromFilesystemConfig{
		Backend: symlinkBackend,
		BaseDir: skillsDir,
	})
	if err != nil {
		t.Fatal("Failed to create skill backend:", err)
	}

	skills, err := backend.List(context.Background())
	if err != nil {
		t.Fatal("Failed to list skills:", err)
	}
	t.Logf("Found %d skills", len(skills))
	for _, s := range skills {
		t.Logf("  - %s: desc=%q", s.Name, s.Description)
	}

	// Compare: get-weather description vs brainstorming description
	for _, s := range skills {
		var reasons []string
		if strings.Contains(s.Description, "当用户") || strings.Contains(s.Description, "when user") {
			reasons = append(reasons, "包含触发条件短语")
		}
		if strings.Contains(s.Description, "使用") || strings.Contains(s.Description, "use this") {
			reasons = append(reasons, "包含使用指令")
		}
		lang := "en"
		for _, r := range []rune(s.Description) {
			if r >= 0x4e00 && r <= 0x9fff {
				lang = "zh"
				break
			}
		}
		reasons = append(reasons, "语言: "+lang)
		t.Logf("  %s -> %v", s.Name, reasons)
	}
}
