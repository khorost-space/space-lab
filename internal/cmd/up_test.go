package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/khorost-space/space-lab/internal/project"
)

// testObjectID — фиктивный object_id, повторяющийся в нескольких тестах:
// goconst иначе просит вынести литерал в константу.
const testObjectID = "019f-объект"

// recordingDeps — заглушка upDeps, пишущая имена вызовов в calls вместо
// исполнения. Позволяет проверить ПОРЯДОК шагов up без Docker.
//
// buildRef и writeComposeRef запоминают АРГУМЕНТЫ, а не только факт вызова:
// живой прогон уже дважды ловил дефект именно в значении ссылки на реестр
// (registry:5000 — имя, которое резолвит только embedded DNS compose внутри
// контейнеров, не демон Docker, пуляющий образ ДО их создания; localhost на
// Windows резолвится в IPv6 первым, а демон включает в insecure-registries
// по умолчанию только 127.0.0.0/8, не ::1) — а заглушка, помнящая только
// «BuildPush вызван», такой откат не ловит вовсе.
type recordingDeps struct {
	calls []string

	objectID  string
	objectErr error
	digest    string
	buildErr  error

	buildRef        string
	writeComposeRef string
}

func (d *recordingDeps) ComposeUp(_ context.Context, services []string) error {
	d.calls = append(d.calls, "compose-up:"+strings.Join(services, ","))
	return nil
}

func (d *recordingDeps) CreateObject(_ context.Context, _, _ string) (string, error) {
	d.calls = append(d.calls, "create-object")
	if d.objectErr != nil {
		return "", d.objectErr
	}
	return d.objectID, nil
}

func (d *recordingDeps) BuildPush(_ context.Context, ref string) (string, error) {
	d.calls = append(d.calls, "build-push")
	d.buildRef = ref
	if d.buildErr != nil {
		return "", d.buildErr
	}
	return d.digest, nil
}

func (d *recordingDeps) WriteCompose(_, _, ref string) error {
	d.calls = append(d.calls, "write-compose")
	d.writeComposeRef = ref
	return nil
}

// TestUpOrdersPhases: объект заводится после подъёма World Core и до подъёма
// Gateway. Gateway читает отображение из конфигурации ПРИ СТАРТЕ, а object_id
// выдаёт платформа — поднять его раньше значит поднять с пустым отображением
// и получать «отображение не найдено» на каждом сигнале.
func TestUpOrdersPhases(t *testing.T) {
	deps := &recordingDeps{objectID: testObjectID, digest: "sha256:" + strings.Repeat("b", 64)}
	if err := upWith(context.Background(), project.Default("vega-0"), deps, io.Discard); err != nil {
		t.Fatalf("upWith: %v", err)
	}
	want := []string{
		"compose-up:postgres,redis,platform-api,platform-worker",
		"create-object",
		"compose-up:registry",
		"build-push",
		"write-compose",
		"compose-up:platform-api",
		"compose-up:dev-issuer,student-gateway,registry,spacecraft",
	}
	if !slices.Equal(deps.calls, want) {
		t.Errorf("порядок шагов:\n получено %v\n ожидалось %v", deps.calls, want)
	}
}

// TestUpRestartsPlatformAPIAfterSecondRender: второй рендер дописывает
// platform-api KHOROST_SHOWCASE_OBJECT_ID, а сама служба поднята ещё первой
// фазой — docker compose up -d на второй фазе её не трогает, её нет в
// PhaseTwo. Без повторного up платформа так и останется без витрины, и
// status/check не заработают никогда.
func TestUpRestartsPlatformAPIAfterSecondRender(t *testing.T) {
	deps := &recordingDeps{objectID: testObjectID, digest: "sha256:" + strings.Repeat("b", 64)}
	if err := upWith(context.Background(), project.Default("vega-0"), deps, io.Discard); err != nil {
		t.Fatalf("upWith: %v", err)
	}
	writeIdx := slices.Index(deps.calls, "write-compose")
	restartIdx := slices.Index(deps.calls, "compose-up:platform-api")
	if writeIdx == -1 || restartIdx == -1 || restartIdx <= writeIdx {
		t.Errorf("platform-api не перезапущен после второго рендера: %v", deps.calls)
	}
	phaseTwoIdx := slices.Index(deps.calls, "compose-up:dev-issuer,student-gateway,registry,spacecraft")
	if phaseTwoIdx == -1 || phaseTwoIdx <= restartIdx {
		t.Errorf("вторая фаза поднята раньше перезапуска platform-api: %v", deps.calls)
	}
}

// TestUpRaisesRegistryBeforeBuildPush: BuildPush пушит образ аппарата в
// локальный реестр — если реестр не поднят заранее, docker push бьётся о
// ещё не запущенный контейнер («connection refused») на КАЖДОМ подъёме, а
// не изредка. Реестр не входит в PhaseOne (платформе он не нужен) и не
// может ждать PhaseTwo — она поднимается уже после BuildPush.
func TestUpRaisesRegistryBeforeBuildPush(t *testing.T) {
	deps := &recordingDeps{objectID: testObjectID, digest: "sha256:" + strings.Repeat("b", 64)}
	if err := upWith(context.Background(), project.Default("vega-0"), deps, io.Discard); err != nil {
		t.Fatalf("upWith: %v", err)
	}
	registryIdx := slices.Index(deps.calls, "compose-up:registry")
	buildIdx := slices.Index(deps.calls, "build-push")
	if registryIdx == -1 || buildIdx == -1 || registryIdx >= buildIdx {
		t.Errorf("реестр поднят не раньше build-push: %v", deps.calls)
	}
}

// TestUpBuildsAndRunsSpacecraftViaLoopbackRegistry: и ссылка, переданная в
// BuildPush (docker build/push), и ссылка, записанная в compose для запуска
// (WriteCompose), обязаны указывать на 127.0.0.1:<порт реестра из
// конфигурации> — тот же адрес, что виден студенту снаружи.
//
// Живой прогон уже дважды проваливался здесь по-разному:
//   - «registry:5000» (имя службы compose) казалось рабочим, но образ тянет
//     ДЕМОН Docker ещё до того, как контейнер spacecraft создан — а имя
//     службы резолвит только embedded DNS сети compose, доступный лишь
//     контейнерам внутри неё: «lookup registry: no such host» на КАЖДОМ
//     подъёме, не изредка;
//   - «localhost:<порт>» вместо голого IPv4 иногда уводил docker push в
//     HTTPS: демон включает в insecure-registries по умолчанию сеть
//     127.0.0.0/8, но не ::1, а localhost на Windows резолвится в IPv6
//     первым — с обычным HTTP-реестром попытка HTTPS зависает на
//     TLS-таймауте, а не падает сразу.
//
// Тест обязан падать на любой из двух прежних форм.
func TestUpBuildsAndRunsSpacecraftViaLoopbackRegistry(t *testing.T) {
	cfg := project.Default("vega-0")
	deps := &recordingDeps{objectID: testObjectID, digest: "sha256:" + strings.Repeat("b", 64)}
	if err := upWith(context.Background(), cfg, deps, io.Discard); err != nil {
		t.Fatalf("upWith: %v", err)
	}

	wantHost := fmt.Sprintf("127.0.0.1:%d/", cfg.Ports.Registry)
	if !strings.HasPrefix(deps.buildRef, wantHost) {
		t.Errorf("ссылка сборки/пуша = %q, ожидался префикс %q", deps.buildRef, wantHost)
	}
	if !strings.HasPrefix(deps.writeComposeRef, wantHost) {
		t.Errorf("ссылка запуска в compose = %q, ожидался префикс %q", deps.writeComposeRef, wantHost)
	}
}

// TestUpStopsOnObjectFailure: если объект не завёлся, поднимать Gateway
// нельзя — он стартует с пустым отображением и выглядит здоровым.
func TestUpStopsOnObjectFailure(t *testing.T) {
	deps := &recordingDeps{objectErr: errors.New("401")}
	err := upWith(context.Background(), project.Default("vega-0"), deps, io.Discard)
	if err == nil {
		t.Fatal("отказ заведения объекта не остановил подъём")
	}
	if slices.Contains(deps.calls, "compose-up:dev-issuer,student-gateway,registry,spacecraft") {
		t.Error("вторая фаза поднята после отказа")
	}
}

// TestUpStopsOnBuildFailure: сборка/пуш аппарата провалились — вторая фаза
// не поднимается: объект уже заведён, но образа для запуска нет.
func TestUpStopsOnBuildFailure(t *testing.T) {
	deps := &recordingDeps{objectID: testObjectID, buildErr: errors.New("docker build: не вышло")}
	err := upWith(context.Background(), project.Default("vega-0"), deps, io.Discard)
	if err == nil {
		t.Fatal("отказ сборки не остановил подъём")
	}
	if slices.Contains(deps.calls, "write-compose") {
		t.Error("compose перезаписан после отказа сборки")
	}
	if slices.Contains(deps.calls, "compose-up:dev-issuer,student-gateway,registry,spacecraft") {
		t.Error("вторая фаза поднята после отказа сборки")
	}
}

// TestUpNamesMissingDockerfile: шаблон аппарата поставляется без Dockerfile
// намеренно — многоступенчатая сборка и non-root входят в приёмку работы 1.
// Значит первый up каждого студента упирается ровно сюда, и сообщение обязано
// называть файл и то, что его надо написать, а не пересказывать ошибку docker.
func TestUpNamesMissingDockerfile(t *testing.T) {
	dir := t.TempDir()
	cfg := project.Default("vega-0")

	err := checkDockerfile(dir, cfg)
	if err == nil {
		t.Fatal("отсутствующий Dockerfile принят без ошибки")
	}
	got := err.Error()
	for _, want := range []string{cfg.Spacecraft.Dockerfile, "напишите"} {
		if !strings.Contains(got, want) {
			t.Errorf("сообщение %q не содержит %q", got, want)
		}
	}
}

// TestCheckDockerfileAcceptsExisting: существующий файл проверку проходит.
func TestCheckDockerfileAcceptsExisting(t *testing.T) {
	dir := t.TempDir()
	cfg := project.Default("vega-0")
	path := filepath.Join(dir, cfg.Spacecraft.Context, cfg.Spacecraft.Dockerfile)
	if err := os.WriteFile(path, []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatalf("подготовить Dockerfile: %v", err)
	}
	if err := checkDockerfile(dir, cfg); err != nil {
		t.Errorf("существующий Dockerfile отвергнут: %v", err)
	}
}
