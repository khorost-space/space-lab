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
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/khorost-space/space-lab/internal/project"
	"github.com/khorost-space/space-lab/internal/stack"
)

// composeArgs собирает аргументы docker compose.
//
// Вынесено из вызова: порядок и состав флагов — то, что ломается молча
// (compose подхватит не тот файл и поднимет пустой стек), и проверять это
// запуском Docker в юнит-тестах нельзя.
func composeArgs(composeFile string, args ...string) []string {
	return append([]string{"compose", "-f", composeFile}, args...)
}

// composePSArgs собирает аргументы «docker compose ps» для StopAndWait.
//
// Вынесено из вызова тем же приёмом, что и composeArgs: --all ломается
// молча. Живой прогон уже ловил дефект здесь — StopAndWait останавливает
// службу, а ЗАТЕМ читает её код выхода через «ps», но «ps» без --all
// показывает только РАБОТАЮЩИЕ контейнеры. Только что остановленной службы
// в этом множестве уже нет — вывод пуст, и GracefulShutdown вместо кода
// выхода видел «docker compose ps: пустой вывод» на КАЖДОЙ проверке, а не
// эпизодически.
func composePSArgs(composeFile, service string) []string {
	return composeArgs(composeFile, "ps", "--all", "--format", "json", service)
}

// composePSAllArgs собирает аргументы «docker compose ps --all» БЕЗ фильтра
// по имени службы — в отличие от composePSArgs, который читает ровно одну
// службу для StopAndWait. StackServicesDown обязан увидеть состояние ВСЕГО
// стека, а не спрашивать по имени каждую службу отдельно.
func composePSAllArgs(composeFile string) []string {
	return composeArgs(composeFile, "ps", "--all", "--format", "json")
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

// servicePS — минимальная форма вывода `docker compose ps --format json`,
// нужная StackServicesDown: имя службы, её текущее состояние и код выхода.
// ExitCode нужен именно здесь, а не только в composePS у StopAndWait: без
// него одноразовую службу (migrate), успешно отработавшую и вышедшую с 0,
// нельзя отличить по ЭТОМУ полю от неё же, упавшей с ненулевым кодом, — оба
// дают State="exited".
type servicePS struct {
	Service  string `json:"Service"`
	State    string `json:"State"`
	ExitCode int    `json:"ExitCode"`
}

// oneShotServices — множество имён из stack.OneShot для быстрой проверки
// «эта служба обязана завершиться, а не остаться running». Вычисляется один
// раз при инициализации пакета: список одноразовых служб не меняется в
// рантайме, и на каждый разбор вывода docker compose ps пересобирать map
// незачем.
var oneShotServices = func() map[string]bool {
	m := make(map[string]bool, len(stack.OneShot))
	for _, name := range stack.OneShot {
		m[name] = true
	}
	return m
}()

// ErrStackNotUp сигнализирует, что «docker compose ps --all» не вернул ни
// одной службы, хотя compose-файл проекта существует: значит стек не поднят
// (не запускали «up» либо запускали «down») — а не «все службы работают».
//
// Пустой список []string от parseServicesDown сам по себе неотличим от этого
// случая: «нет упавших служб» и «служб вообще нет» дают одно и то же nil-
// значение. Раньше это и было дырой — check.checkWith получал пустой список,
// читал его как «все службы стека подняты» и шёл дальше проверять аппарат,
// которого физически нет, а красный результат проб и сигналов списывался на
// студента. Сигнальная ошибка заставляет вызывающий код отказать так же
// громко, как при упавшей службе, а не молча продолжить.
var ErrStackNotUp = errors.New(
	"стек полигона не поднят: docker compose ps не вернул ни одной службы — выполните «space-lab up»",
)

// parseServicesDown разбирает построчный JSON «docker compose ps --all» и
// возвращает описание каждой службы, находящейся не в ожидаемом для неё
// состоянии.
//
// Стек полигона состоит из двух родов служб (см. stack.PhaseOne,
// stack.PhaseTwo и stack.OneShot), и «упала» означает для них РАЗНОЕ:
//   - для одноразовых (stack.OneShot, сейчас — только migrate): служба
//     ОБЯЗАНА выйти, State="exited" — это норма, а не отказ. Упавшей
//     считается только выход с ненулевым ExitCode — сама миграция не
//     накатилась. State="exited"+ExitCode=0 у неё же — то самое штатное
//     завершение, ради которого platform-api и platform-worker ждут её
//     через service_completed_successfully (см. compose.tmpl); до этой
//     правки State!="running" валило migrate вместе с реально упавшими
//     службами на КАЖДОМ успешном up — check был красным всегда.
//   - для всех остальных, включая spacecraft, — по-прежнему
//     State != "running": долгоживущая служба, вышедшая сама, — отказ вне
//     зависимости от кода выхода (spacecraft штатно останавливает отдельная
//     проверка check.GracefulShutdown, которая идёт последней и намеренно
//     переводит его в exited уже ПОСЛЕ того, как StackServicesDown отработал).
//
// Вынесено из StackServicesDown тем же приёмом, что parseDigest из
// BuildPush: разбор вывода — то, что ломается молча и проверяется юнит-тестом
// без Docker, а сам запуск — только e2e.
func parseServicesDown(out string) ([]string, error) {
	trimmed := strings.TrimSpace(out)
	if trimmed == "" {
		return nil, ErrStackNotUp
	}
	var down []string
	for _, line := range strings.Split(trimmed, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var svc servicePS
		if err := json.Unmarshal([]byte(line), &svc); err != nil {
			return nil, fmt.Errorf("разобрать docker compose ps: %w", err)
		}
		if oneShotServices[svc.Service] {
			if svc.State == "exited" && svc.ExitCode == 0 {
				continue
			}
			down = append(down, fmt.Sprintf("%s (%s, код выхода %d)", svc.Service, svc.State, svc.ExitCode))
			continue
		}
		if svc.State != "running" {
			down = append(down, fmt.Sprintf("%s (%s)", svc.Service, svc.State))
		}
	}
	return down, nil
}

// StackServicesDown возвращает описание служб стека, находящихся не в
// ожидаемом для них состоянии (см. parseServicesDown — критерий разный для
// долгоживущих служб и одноразовых из stack.OneShot), по данным
// «docker compose ps --all».
//
// Список служб стека не передаётся: смотрим на всё, что подняла compose, а
// не на заранее известный набор имён — иначе служба, про которую вызывающий
// код забыл явно спросить, могла бы упасть незамеченной. Пустой вывод — уже
// НЕ трактуется как «всё стоит» (см. ErrStackNotUp): «space-lab down» оставляет
// compose-файл на месте, но не поднимает ни одного контейнера, и молчаливое
// «упавших служб нет» в этом случае было бы неправдой.
func StackServicesDown(ctx context.Context, dir string) ([]string, error) {
	ps := exec.CommandContext(ctx, "docker", composePSAllArgs(composeFile(dir))...)
	var out bytes.Buffer
	ps.Stdout = &out
	ps.Stderr = &out
	if err := ps.Run(); err != nil {
		return nil, fmt.Errorf("docker compose ps: %w: %s", err, out.String())
	}
	return parseServicesDown(out.String())
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

	ps := exec.CommandContext(ctx, "docker", composePSArgs(composeFile(dir), service)...)
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
