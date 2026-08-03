package cmd_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/khorost-space/space-lab/internal/cmd"
	"github.com/khorost-space/space-lab/internal/project"
)

// TestSecretsAreStableAcrossCalls: токены переживают перезапуск up. Новые
// токены на каждом подъёме означали бы, что уже поднятый Gateway ходит в
// platform-api со старым и получает 401.
func TestSecretsAreStableAcrossCalls(t *testing.T) {
	dir := t.TempDir()
	if _, err := project.Init(dir, "vega-0"); err != nil {
		t.Fatalf("Init: %v", err)
	}
	first, err := cmd.LoadOrCreateSecrets(dir)
	if err != nil {
		t.Fatalf("первый вызов: %v", err)
	}
	second, err := cmd.LoadOrCreateSecrets(dir)
	if err != nil {
		t.Fatalf("второй вызов: %v", err)
	}
	if first != second {
		t.Errorf("токены пересозданы: %+v против %+v", first, second)
	}
	if first.APIToken == first.GatewayToken {
		t.Error("токен /objects совпал с токеном Gateway: компрометация Gateway дала бы права на объекты мира")
	}
}

// TestSecretsFileIsNotWorldReadable: в файле лежат оба токена.
func TestSecretsFileIsNotWorldReadable(t *testing.T) {
	dir := t.TempDir()
	if _, err := project.Init(dir, "vega-0"); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if _, err := cmd.LoadOrCreateSecrets(dir); err != nil {
		t.Fatalf("LoadOrCreateSecrets: %v", err)
	}
	info, err := os.Stat(filepath.Join(dir, project.StateDir, "secrets.json"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Errorf("права %v открывают файл посторонним", info.Mode().Perm())
	}
}

// TestServiceAccountDerivesFromObjectID: имя ServiceAccount выводится из
// object_id — то же соглашение, что в кластере (object-<id>). Одно значение в
// нескольких местах, и расхождение даёт «отображение не найдено».
func TestServiceAccountDerivesFromObjectID(t *testing.T) {
	got := cmd.ServiceAccountFor("019f7f3a-127c-7b25-a8d3-e049ffbba58f")
	if !strings.HasPrefix(got, "object-") {
		t.Errorf("имя = %q, ожидался префикс object-", got)
	}
	if got != cmd.ServiceAccountFor("019f7f3a-127c-7b25-a8d3-e049ffbba58f") {
		t.Error("имя не детерминировано")
	}
}
