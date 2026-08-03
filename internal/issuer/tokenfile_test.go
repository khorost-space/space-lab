package issuer_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/khorost-space/space-lab/internal/issuer"
)

// TestWriteTokenFileHasNoTrailingNewline: аппарат кладёт содержимое файла в
// заголовок Authorization. Хвостовой перевод строки Gateway отвергнет как
// неверный токен, и отказ будет выглядеть как ошибка кода студента.
func TestWriteTokenFileHasNoTrailingNewline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	if err := issuer.WriteTokenFile(path, "a.b.c"); err != nil {
		t.Fatalf("WriteTokenFile: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("прочитать: %v", err)
	}
	if strings.HasSuffix(string(raw), "\n") {
		t.Errorf("файл токена оканчивается переводом строки: %q", raw)
	}
	if string(raw) != "a.b.c" {
		t.Errorf("содержимое = %q", raw)
	}
}

// TestWriteTokenFileIsAtomic: аппарат читает файл на каждой отправке, и
// ротация не должна давать ему прочитать половину токена. Перезапись идёт
// через временный файл и rename.
func TestWriteTokenFileIsAtomic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	if err := issuer.WriteTokenFile(path, "первый"); err != nil {
		t.Fatalf("первая запись: %v", err)
	}
	if err := issuer.WriteTokenFile(path, "второй"); err != nil {
		t.Fatalf("вторая запись: %v", err)
	}
	raw, _ := os.ReadFile(path)
	if string(raw) != "второй" {
		t.Errorf("содержимое = %q, ожидалось «второй»", raw)
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Errorf("в каталоге %d файлов, временный не убран", len(entries))
	}
}
