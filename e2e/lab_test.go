//go:build e2e

// Package e2e_test прогоняет полигон целиком: от «git clone» эталонного
// аппарата до зелёного «space-lab check» через реальный Docker.
//
// Тег сборки e2e держит юнит-тесты (go test ./...) быстрыми и не требующими
// Docker: без тега этот пакет не собирается вовсе, и обычный прогон его не
// видит.
//
// Это не только проверка space-lab, но и СТРАЖ КОНТРАКТА между полигоном и
// платформой. Если платформа изменит форму heartbeat, путь файла токена,
// audience Gateway или коды его ошибок — красным станет этот тест, а не
// прогон живого студента, который до сих пор был единственным способом
// узнать о расхождении.
package e2e_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/khorost-space/space-lab/internal/project"
)

const (
	// referenceRepo — эталонный аппарат, на котором проверяется полигон.
	referenceRepo = "https://github.com/khorost-space/vega-0.git"
	// referenceCommit закрепляет ТОЧНЫЙ коммит эталона: «ci: свести
	// actions/checkout к одной версии», 2026-07-21. Плавающая ветка вместо
	// коммита означала бы, что содержание стража меняется без правки этого
	// файла — молча, чужим коммитом в чужом репозитории.
	referenceCommit = "5bb8588f07c2cac09858cf642d53a97ef913cb19"

	// onlineTimeout — сколько ждём выхода аппарата на online после «up».
	// Первый сигнал уходит сразу после старта аппарата, но подъём postgres,
	// миграций и Gateway занимает десятки секунд.
	onlineTimeout = 3 * time.Minute
)

// TestLabRaisesReferenceSpacecraft поднимает полигон с эталонным аппаратом и
// доводит его до online, затем прогоняет «check» и проверяет, что отчёт
// называет все три класса паритета (ADR-0020) и не проваливает ни одного
// результата класса guaranteed.
func TestLabRaisesReferenceSpacecraft(t *testing.T) {
	root := repoRoot(t)
	bin := buildLabBinary(t, root)
	buildIssuerImage(t, root)

	dir := t.TempDir()
	cloneReference(t, dir)

	runCLI(t, bin, dir, "init", "-name", "vega-0")
	t.Cleanup(func() {
		// Best-effort: если «up» упал на середине, часть стека всё равно
		// могла подняться, и её нужно убрать. Ошибку здесь не считаем
		// провалом теста — сам тест уже сказал своё слово раньше.
		out, err := runCLICapture(t, bin, dir, "down", "-purge")
		if err != nil {
			t.Logf("space-lab down -purge: %v\n%s", err, out)
		}
	})

	runCLI(t, bin, dir, "up")

	waitOnline(t, bin, dir, onlineTimeout)

	out, checkErr := runCLICapture(t, bin, dir, "check")
	for _, want := range []string{"guaranteed", "environment-dependent", "central-only"} {
		if !strings.Contains(out, want) {
			t.Errorf("в отчёте check нет класса %q:\n%s", want, out)
		}
	}
	// Ненулевой код возврата «check» означает провал результата класса
	// guaranteed (ADR-0020) — а именно guaranteed обязан совпасть локально
	// и централизованно. Провал здесь — не шум ноутбука, а расхождение
	// контракта, ради обнаружения которого весь этот тест и существует.
	if checkErr != nil {
		t.Errorf("space-lab check провалил результат класса guaranteed:\n%s", out)
	}
}

// repoRoot находит корень модуля space-lab по расположению пакета e2e_test:
// «go test» запускает тестовый бинарник с рабочим каталогом пакета, а
// каталог e2e/ — прямой потомок корня.
func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("узнать рабочий каталог: %v", err)
	}
	root := filepath.Dir(wd)
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("не нашли go.mod в предполагаемом корне репозитория %s: %v", root, err)
	}
	return root
}

// buildLabBinary собирает cmd/space-lab во временный файл: e2e гоняет тот же
// бинарник, который получает студент, а не вызывает internal-пакеты
// напрямую — иначе тест проверял бы не тот путь, каким на самом деле идёт
// разбор CLI-флагов.
func buildLabBinary(t *testing.T, root string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "space-lab")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	cmd := exec.CommandContext(context.Background(), "go", "build", "-o", bin, "./cmd/space-lab")
	cmd.Dir = root
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("go build ./cmd/space-lab: %v\n%s", err, out.String())
	}
	return bin
}

// issuerLocalTag выводит тег локально собранного образа dev-issuer из
// project.Default — того же умолчания, которое подставляет compose.tmpl
// (platform.issuer_version) при рендере стека. Тег выводится из
// конфигурации, а не зашит литералом: смени умолчание в project.Default —
// и этот тест продолжит собирать ИМЕННО ТОТ образ, который «up» реально
// будет ждать в локальном кэше, а не молча разойдётся с ним.
//
// docker compose up по умолчанию не идёт в реестр за образом, который уже
// есть в локальном кэше демона под тем же именем и тегом (pull_policy:
// missing) — образ ghcr.io/khorost-space/space-lab-issuer ещё никем не
// публиковался (задача 11 первая, кто его публикует), и без этого приёма
// «up» вовсе не поднимется. Тот же приём пригоден и студенту: сеть и токен
// к GHCR для issuer'а не нужны.
func issuerLocalTag() string {
	cfg := project.Default("vega-0")
	return fmt.Sprintf("ghcr.io/khorost-space/space-lab-issuer:%s", cfg.Platform.IssuerVersion)
}

// buildIssuerImage собирает образ dev-issuer из Dockerfile.issuer и
// тегирует его как issuerLocalTag() — см. комментарий там же про приём с
// pull_policy=missing.
func buildIssuerImage(t *testing.T, root string) {
	t.Helper()
	cmd := exec.CommandContext(context.Background(), "docker", "build", "-f", "Dockerfile.issuer", "-t", issuerLocalTag(), ".")
	cmd.Dir = root
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("docker build -f Dockerfile.issuer: %v", err)
	}
}

// cloneReference клонирует эталонный аппарат и переключает его на закреплённый
// referenceCommit.
//
// Клон по сети, а не локальная копия репозитория с диска разработчика: CI-
// раннер такой копии не видит, а тест обязан быть воспроизводим и там, и
// на машине студента.
func cloneReference(t *testing.T, dir string) {
	t.Helper()
	clone := exec.CommandContext(context.Background(), "git", "clone", "--quiet", referenceRepo, dir)
	var out bytes.Buffer
	clone.Stdout, clone.Stderr = &out, &out
	if err := clone.Run(); err != nil {
		t.Fatalf("git clone %s: %v\n%s", referenceRepo, err, out.String())
	}

	out.Reset()
	checkout := exec.CommandContext(context.Background(), "git", "-C", dir, "checkout", "--quiet", referenceCommit)
	checkout.Stdout, checkout.Stderr = &out, &out
	if err := checkout.Run(); err != nil {
		t.Fatalf("git checkout %s: %v\n%s", referenceCommit, err, out.String())
	}
}

// runCLICapture запускает bin с args в каталоге dir и возвращает
// объединённый вывод и ошибку запуска без остановки теста — вызывающий код
// сам решает, фатальна ли она (см. runCLI и обработку «check» в самом
// тесте, где ненулевой код возврата — часть проверяемого поведения).
//
// context.Background(), а не t.Context(): эту функцию зовут и из
// t.Cleanup(func() { ... down -purge ... }) в самом тесте — а t.Context()
// отменяется ДО запуска функций Cleanup, и «down» с уже отменённым
// контекстом не запустился бы вовсе. Общий срок жизни всей команде и так
// даёт `go test -timeout`.
func runCLICapture(t *testing.T, bin, dir string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.CommandContext(context.Background(), bin, args...)
	cmd.Dir = dir
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	return out.String(), err
}

// runCLI — то же самое, что runCLICapture, но останавливает тест при
// ненулевом коде возврата: для init/up/status используется именно этот
// вариант, потому что там любой отказ — уже дефект, а не наблюдение.
func runCLI(t *testing.T, bin, dir string, args ...string) string {
	t.Helper()
	out, err := runCLICapture(t, bin, dir, args...)
	if err != nil {
		t.Fatalf("space-lab %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

// waitOnline опрашивает «space-lab status», пока в выводе не появится
// «Состояние: online», либо пока не истечёт timeout.
func waitOnline(t *testing.T, bin, dir string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last string
	for {
		last = runCLI(t, bin, dir, "status")
		if strings.Contains(last, "Состояние: online") {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("аппарат не вышел на online за %s, последний статус:\n%s", timeout, last)
		}
		time.Sleep(2 * time.Second)
	}
}
