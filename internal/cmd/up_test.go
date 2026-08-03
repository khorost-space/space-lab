package cmd

import (
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/khorost-space/space-lab/internal/project"
)

// recordingDeps — заглушка upDeps, пишущая имена вызовов в calls вместо
// исполнения. Позволяет проверить ПОРЯДОК шагов up без Docker.
type recordingDeps struct {
	calls []string

	objectID  string
	objectErr error
	digest    string
	buildErr  error
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

func (d *recordingDeps) BuildPush(_ context.Context, _ string) (string, error) {
	d.calls = append(d.calls, "build-push")
	if d.buildErr != nil {
		return "", d.buildErr
	}
	return d.digest, nil
}

func (d *recordingDeps) WriteCompose(_, _, _ string) error {
	d.calls = append(d.calls, "write-compose")
	return nil
}

// TestUpOrdersPhases: объект заводится после подъёма World Core и до подъёма
// Gateway. Gateway читает отображение из конфигурации ПРИ СТАРТЕ, а object_id
// выдаёт платформа — поднять его раньше значит поднять с пустым отображением
// и получать «отображение не найдено» на каждом сигнале.
func TestUpOrdersPhases(t *testing.T) {
	deps := &recordingDeps{objectID: "019f-объект", digest: "sha256:" + strings.Repeat("b", 64)}
	if err := upWith(context.Background(), project.Default("vega-0"), deps, io.Discard); err != nil {
		t.Fatalf("upWith: %v", err)
	}
	want := []string{
		"compose-up:postgres,redis,platform-api,platform-worker",
		"create-object",
		"build-push",
		"write-compose",
		"compose-up:dev-issuer,student-gateway,registry,spacecraft",
	}
	if !slices.Equal(deps.calls, want) {
		t.Errorf("порядок шагов:\n получено %v\n ожидалось %v", deps.calls, want)
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
	deps := &recordingDeps{objectID: "019f-объект", buildErr: errors.New("docker build: не вышло")}
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
