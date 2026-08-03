package project

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Init готовит проект к работе полигона: конфигурация, каталог состояния,
// правило в .gitignore.
//
// Идемпотентен. Существующую конфигурацию НЕ переписывает: в ней уже могли
// быть правки студента, а «init» — не «reset». Молча затёртые порты дали бы
// необъяснимый отказ на следующем up.
func Init(dir, name string) (Config, error) {
	if err := os.MkdirAll(filepath.Join(dir, StateDir), 0o755); err != nil {
		return Config{}, fmt.Errorf("создать %s: %w", StateDir, err)
	}
	if err := ensureGitignore(dir); err != nil {
		return Config{}, err
	}

	if c, err := Load(dir); err == nil {
		return c, nil
	}
	c := Default(name)
	if err := Save(dir, c); err != nil {
		return Config{}, err
	}
	return c, nil
}

// ensureGitignore дописывает правило, если его там ещё нет.
//
// В .space-lab/ лежат локальный API-токен и ключ подписи dev-issuer.
// Отсутствие правила — это опубликованный ключ в репозитории студента, и
// обнаружился бы он на ревью преподавателем, а не раньше.
func ensureGitignore(dir string) error {
	path := filepath.Join(dir, ".gitignore")
	rule := StateDir + "/"

	raw, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("прочитать .gitignore: %w", err)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(line) == rule {
			return nil
		}
	}

	body := string(raw)
	if body != "" && !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	body += rule + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return fmt.Errorf("записать .gitignore: %w", err)
	}
	return nil
}
