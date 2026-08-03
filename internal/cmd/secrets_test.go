package cmd_test

import (
	"os"
	"path/filepath"
	"runtime"
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
//
// На Windows содержательная часть пропускается: NTFS не исполняет POSIX-биты
// доступа, os.WriteFile(0o600) там не сужает права так же, как на Linux, и
// info.Mode().Perm() возвращает не то, что проверяет этот тест. Разработка
// ведётся с Windows, а CI — Linux (.github/workflows: runs-on Linux); там
// проверка идёт по прямому назначению. Красный без возможности когда-либо
// стать зелёным на машине разработчика — это шум, а не сигнал, поэтому
// смысл теста сохраняется только для POSIX.
func TestSecretsFileIsNotWorldReadable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("права POSIX не действуют на NTFS; проверяется в CI на Linux")
	}
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
