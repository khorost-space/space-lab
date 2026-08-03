package issuer

import (
	"fmt"
	"os"
	"path/filepath"
)

// WriteTokenFile атомарно записывает токен по пути path.
//
// Атомарность обязательна: аппарат читает файл на КАЖДОЙ отправке (так
// устроен эталон, потому что kubelet ротирует токен), и попадание на
// полузаписанный файл дало бы отказ Gateway, воспроизводящийся раз в десять
// минут и необъяснимый по логам аппарата.
//
// Перевод строки не дописывается: содержимое идёт в заголовок Authorization
// как есть.
func WriteTokenFile(path, token string) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".token-*")
	if err != nil {
		return fmt.Errorf("создать временный файл токена: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := tmp.WriteString(token); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("записать токен: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("закрыть временный файл токена: %w", err)
	}
	// 0o600: токен читает только процесс аппарата, смонтировавший том.
	if err := os.Chmod(tmpName, 0o600); err != nil {
		return fmt.Errorf("права на файл токена: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("переместить файл токена: %w", err)
	}
	return nil
}
