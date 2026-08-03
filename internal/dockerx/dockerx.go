// Package dockerx — узкая обёртка над бинарями docker и docker compose.
//
// Полигон не переизобретает клиент Docker: он запускает готовые команды и
// разбирает то немногое, что ему нужно из их вывода. Тестируется здесь
// только то, что ломается тихо, — сборка аргументов и разбор вывода; сам
// запуск Docker проверяется e2e, не юнит-тестами.
package dockerx

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/khorost-space/space-lab/internal/project"
)

// composeArgs собирает аргументы docker compose.
//
// Вынесено из вызова: порядок и состав флагов — то, что ломается молча
// (compose подхватит не тот файл и поднимет пустой стек), и проверять это
// запуском Docker в юнит-тестах нельзя.
func composeArgs(composeFile string, args ...string) []string {
	return append([]string{"compose", "-f", composeFile}, args...)
}

// digestRe ищет голый sha256:<64hex> в выводе docker push.
var digestRe = regexp.MustCompile(`sha256:[0-9a-f]{64}`)

// parseDigest достаёт голый sha256:<64hex>.
//
// Именно голый: доставка платформы уже ломалась на том, что инструмент писал
// полную ссылку ref@sha256:…, а потребитель ждал digest. Расхождение
// проявляется не при сборке, а на сверке digest — далеко от причины.
func parseDigest(out string) (string, error) {
	m := digestRe.FindString(out)
	if m == "" {
		return "", fmt.Errorf("в выводе docker push нет digest: %q", out)
	}
	return m, nil
}

// composeFile — путь к закреплённому compose-файлу студенческого проекта.
func composeFile(dir string) string {
	return filepath.Join(dir, project.StateDir, "docker-compose.yaml")
}

// Compose запускает docker compose с закреплённым файлом каталога проекта.
//
// Stdout/Stderr подключены к потоку самой команды space-lab: студент должен
// видеть, что происходит (сборка образов, подъём служб), а не смотреть в
// замерший курсор.
func Compose(ctx context.Context, dir string, args ...string) error {
	cmd := exec.CommandContext(ctx, "docker", composeArgs(composeFile(dir), args...)...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker %s: %w", strings.Join(cmd.Args[1:], " "), err)
	}
	return nil
}

// BuildPush собирает образ и пушит его в реестр, возвращая заявленный digest.
func BuildPush(ctx context.Context, buildCtx, dockerfile, ref string) (string, error) {
	build := exec.CommandContext(ctx, "docker", "build", "-f", dockerfile, "-t", ref, buildCtx)
	build.Stdout = os.Stdout
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		return "", fmt.Errorf("docker build: %w", err)
	}

	push := exec.CommandContext(ctx, "docker", "push", ref)
	var out bytes.Buffer
	push.Stdout = &out
	push.Stderr = &out
	if err := push.Run(); err != nil {
		return "", fmt.Errorf("docker push: %w: %s", err, out.String())
	}

	digest, err := parseDigest(out.String())
	if err != nil {
		return "", fmt.Errorf("docker push %s: %w", ref, err)
	}
	return digest, nil
}

// ImageUser читает объявленного пользователя образа (Config.User) — то, под
// каким UID/именем реально стартует контейнер аппарата.
func ImageUser(ctx context.Context, ref string) (string, error) {
	cmd := exec.CommandContext(ctx, "docker", "image", "inspect", "--format", "{{.Config.User}}", ref)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("docker image inspect %s: %w: %s", ref, err, out.String())
	}
	return strings.TrimSpace(out.String()), nil
}

// composePS — минимальная форма вывода `docker compose ps --format json`,
// нужная только для кода выхода остановленной службы.
type composePS struct {
	ExitCode int `json:"ExitCode"`
}

// StopAndWait останавливает службу compose и дожидается кода выхода.
//
// timeout передаётся в docker compose stop -t: студент вправе задать, сколько
// ждать штатной остановки, прежде чем Docker пришлёт SIGKILL.
func StopAndWait(ctx context.Context, dir, service string, timeout time.Duration) (int, time.Duration, error) {
	seconds := int(timeout.Seconds())
	start := time.Now()

	stop := exec.CommandContext(ctx, "docker",
		composeArgs(composeFile(dir), "stop", "-t", fmt.Sprintf("%d", seconds), service)...)
	stop.Stdout = os.Stdout
	stop.Stderr = os.Stderr
	if err := stop.Run(); err != nil {
		return 0, time.Since(start), fmt.Errorf("docker compose stop %s: %w", service, err)
	}
	elapsed := time.Since(start)

	ps := exec.CommandContext(ctx, "docker", composeArgs(composeFile(dir), "ps", "--format", "json", service)...)
	var out bytes.Buffer
	ps.Stdout = &out
	ps.Stderr = &out
	if err := ps.Run(); err != nil {
		return 0, elapsed, fmt.Errorf("docker compose ps %s: %w: %s", service, err, out.String())
	}

	line := strings.TrimSpace(out.String())
	if line == "" {
		return 0, elapsed, fmt.Errorf("docker compose ps %s: пустой вывод", service)
	}
	// `ps --format json` печатает по одному JSON-объекту в строке; службе,
	// которую только что остановили, соответствует ровно одна строка, поэтому
	// разбирается только первая.
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}
	var st composePS
	if err := json.Unmarshal([]byte(line), &st); err != nil {
		return 0, elapsed, fmt.Errorf("разобрать docker compose ps %s: %w", service, err)
	}
	return st.ExitCode, elapsed, nil
}
