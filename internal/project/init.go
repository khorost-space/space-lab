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
//
// Файл различается по наличию, а не по успеху Load: если space-lab.yaml уже
// есть, но не читается или не проходит валидацию, это тоже НЕ повод его
// затирать — Default() поверх такого файла спрятал бы опечатку студента под
// чистым листом вместо того, чтобы дать её исправить.
func Init(dir, name string) (Config, error) {
	// 0o700, а не 0o644/0o755: внутри .space-lab/ лежат API-токен, токен
	// Gateway и ключ подписи dev-issuer открытым текстом (см. Write в
	// stack/compose.go и ensureGitignore ниже) — читать и заходить в
	// каталог должен только сам студент.
	if err := os.MkdirAll(filepath.Join(dir, StateDir), 0o700); err != nil {
		return Config{}, fmt.Errorf("создать %s: %w", StateDir, err)
	}
	if err := ensureGitignore(dir); err != nil {
		return Config{}, err
	}

	path := filepath.Join(dir, ConfigFile)
	_, statErr := os.Stat(path)
	switch {
	case statErr == nil:
		// Файл уже есть: Load решает, годится ли он, но в любом случае Init
		// его не трогает — ни при успехе (там могут быть правки студента),
		// ни при ошибке (студенту нужно увидеть и исправить свою строку, а
		// не получить её обратно перетёртой умолчаниями).
		c, loadErr := Load(dir)
		if loadErr != nil {
			return Config{}, fmt.Errorf("существующий %s не тронут: %w", ConfigFile, loadErr)
		}
		return c, nil
	case !os.IsNotExist(statErr):
		return Config{}, fmt.Errorf("проверить %s: %w", ConfigFile, statErr)
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
