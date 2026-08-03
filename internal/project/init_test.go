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

// TestInitDoesNotOverwriteInvalidConfig: невалидный space-lab.yaml (опечатка,
// занятый дважды порт) — тоже не повод для «reset». Init обязан отказать и
// оставить файл студенту на починку, а не подменить его чистыми умолчаниями.
func TestInitDoesNotOverwriteInvalidConfig(t *testing.T) {
	dir := t.TempDir()
	if _, err := project.Init(dir, "vega-0"); err != nil {
		t.Fatalf("Init: %v", err)
	}
	c, _ := project.Load(dir)
	c.Ports.Gateway = c.Ports.API // Save не валидирует — так файл станет невалидным на диске.
	if err := project.Save(dir, c); err != nil {
		t.Fatalf("Save: %v", err)
	}

	path := filepath.Join(dir, project.ConfigFile)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("прочитать %s: %v", project.ConfigFile, err)
	}

	_, err = project.Init(dir, "vega-0")
	if err == nil {
		t.Fatal("Init принял невалидный конфиг молча")
	}
	if !strings.Contains(err.Error(), project.ConfigFile) {
		t.Errorf("ошибка не называет файл: %q", err)
	}

	after, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("прочитать %s после Init: %v", project.ConfigFile, readErr)
	}
	if string(before) != string(after) {
		t.Errorf("Init изменил невалидный файл: было %q, стало %q", before, after)
	}
}
