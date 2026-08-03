package project_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/khorost-space/space-lab/internal/project"
)

// TestInitCreatesConfigStateAndGitignore: init создаёт конфигурацию, каталог
// состояния и правило в .gitignore.
func TestInitCreatesConfigStateAndGitignore(t *testing.T) {
	dir := t.TempDir()
	if _, err := project.Init(dir, "vega-0"); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, project.ConfigFile)); err != nil {
		t.Errorf("нет %s: %v", project.ConfigFile, err)
	}
	if _, err := os.Stat(filepath.Join(dir, project.StateDir)); err != nil {
		t.Errorf("нет %s: %v", project.StateDir, err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatalf(".gitignore не создан: %v", err)
	}
	if !strings.Contains(string(raw), project.StateDir+"/") {
		t.Errorf(".gitignore не игнорирует %s: %q", project.StateDir, raw)
	}
}

// TestInitDoesNotDuplicateGitignoreRule: повторный init не дописывает правило
// второй раз.
func TestInitDoesNotDuplicateGitignoreRule(t *testing.T) {
	dir := t.TempDir()
	if _, err := project.Init(dir, "vega-0"); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if _, err := project.Init(dir, "vega-0"); err != nil {
		t.Fatalf("повторный Init: %v", err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if n := strings.Count(string(raw), project.StateDir+"/"); n != 1 {
		t.Errorf("правило встречается %d раз, ожидался 1", n)
	}
}

// TestInitDoesNotOverwriteExistingConfig: у студента там могли быть правки, и
// молча затереть их хуже, чем отказать.
func TestInitDoesNotOverwriteExistingConfig(t *testing.T) {
	dir := t.TempDir()
	if _, err := project.Init(dir, "vega-0"); err != nil {
		t.Fatalf("Init: %v", err)
	}
	c, _ := project.Load(dir)
	c.Ports.API = 19999
	if err := project.Save(dir, c); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := project.Init(dir, "другое-имя"); err != nil {
		t.Fatalf("повторный Init: %v", err)
	}
	got, _ := project.Load(dir)
	if got.Ports.API != 19999 {
		t.Errorf("init затёр правки студента: ports.api = %d", got.Ports.API)
	}
}
