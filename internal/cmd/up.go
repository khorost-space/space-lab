package cmd

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/khorost-space/space-lab/internal/dockerx"
	"github.com/khorost-space/space-lab/internal/project"
	"github.com/khorost-space/space-lab/internal/stack"
	"github.com/khorost-space/space-lab/internal/worldapi"
)

// namespace — пространство имён токенов dev-issuer полигона.
//
// Значение то же, что defaultNamespace в cmd/space-lab-issuer: одна
// константа в двух местах уже разошлась бы «отображение не найдено» в
// Gateway, а разойдись она здесь — ту же ошибку получит каждый студент.
const namespace = "space-lab"

// upDeps — внешние действия, которые выполняет up.
//
// Интерфейс, а не прямые вызовы: последовательность фаз — то, что обязано
// быть проверено (объект заводится ПОСЛЕ platform-api и ДО Gateway), а
// проверить её запуском Docker в юнит-тесте нельзя.
type upDeps interface {
	ComposeUp(ctx context.Context, services []string) error
	CreateObject(ctx context.Context, name, owner string) (string, error)
	BuildPush(ctx context.Context, ref string) (digest string, err error)
	WriteCompose(objectID, digest, ref string) error
}

// upWith выполняет подъём стека в фиксированном порядке: сначала платформа
// (фаза 1), затем объект в мире, затем сборка и публикация образа аппарата,
// затем перезапись compose с реальным object_id и, наконец, фаза 2
// (идентичность, Gateway, реестр, сам аппарат).
//
// Порядок пиннится тестами (TestUpOrdersPhases и соседние), а не
// комментарием: Gateway читает отображение из конфигурации ПРИ СТАРТЕ, а
// object_id выдаёт платформа — поднять Gateway раньше значит поднять его с
// пустым отображением.
func upWith(ctx context.Context, cfg project.Config, deps upDeps, stdout io.Writer) error {
	_, _ = fmt.Fprintln(stdout, "Фаза 1: платформа (postgres, redis, platform-api, platform-worker)")
	if err := deps.ComposeUp(ctx, stack.PhaseOne); err != nil {
		return fmt.Errorf("поднять первую фазу: %w", err)
	}

	_, _ = fmt.Fprintln(stdout, "Заводим объект в мире")
	objectID, err := deps.CreateObject(ctx, cfg.Object.Name, cfg.Object.Owner)
	if err != nil {
		return fmt.Errorf("завести объект: %w", err)
	}

	// localhost:<port> — снаружи, с машины студента, куда docker build и
	// docker push обращаются напрямую через опубликованный порт реестра.
	buildRef := fmt.Sprintf("localhost:%d/spacecraft:local", cfg.Ports.Registry)
	_, _ = fmt.Fprintln(stdout, "Собираем и публикуем образ аппарата")
	digest, err := deps.BuildPush(ctx, buildRef)
	if err != nil {
		return fmt.Errorf("собрать и опубликовать образ аппарата: %w", err)
	}

	// registry:5000 — изнутри сети compose, куда служба spacecraft обращается
	// по имени соседней службы и её порту ВНУТРИ контейнера, а не по
	// опубликованному наружу localhost:<port>. Имя хоста и порт различаются
	// намеренно, это не опечатка.
	runImage := fmt.Sprintf("registry:5000/spacecraft@%s", digest)
	if err := deps.WriteCompose(objectID, digest, runImage); err != nil {
		return fmt.Errorf("подготовить compose второй фазы: %w", err)
	}

	_, _ = fmt.Fprintln(stdout, "Фаза 2: идентичность, Gateway, реестр, аппарат")
	if err := deps.ComposeUp(ctx, stack.PhaseTwo); err != nil {
		return fmt.Errorf("поднять вторую фазу: %w", err)
	}
	return nil
}

// Up выполняет «space-lab up» в каталоге dir: собирает боевые зависимости
// (worldapi.Client, dockerx, stack.Write) и вызывает upWith.
func Up(ctx context.Context, dir string, stdout io.Writer) error {
	cfg, err := project.Load(dir)
	if err != nil {
		return err
	}
	secrets, err := LoadOrCreateSecrets(dir)
	if err != nil {
		return err
	}

	// Первый рендер — пустым ObjectID: до подъёма platform-api объекта в мире
	// ещё не существует, а без файла compose нечем поднимать даже первую
	// фазу (docker compose требует существующий файл).
	first := stack.Params{
		Cfg:          cfg,
		APIToken:     secrets.APIToken,
		GatewayToken: secrets.GatewayToken,
		Namespace:    namespace,
	}
	if err := stack.Write(dir, first); err != nil {
		return fmt.Errorf("подготовить compose первой фазы: %w", err)
	}

	hc := &http.Client{Timeout: 5 * time.Second}
	deps := &liveUpDeps{
		dir:     dir,
		cfg:     cfg,
		secrets: secrets,
		hc:      hc,
		world:   worldapi.New(fmt.Sprintf("http://localhost:%d", cfg.Ports.API), secrets.APIToken, hc),
	}
	return upWith(ctx, cfg, deps, stdout)
}

// liveUpDeps — боевая реализация upDeps поверх dockerx, worldapi и
// stack.Write.
type liveUpDeps struct {
	dir     string
	cfg     project.Config
	secrets Secrets
	hc      *http.Client
	world   *worldapi.Client
}

func (d *liveUpDeps) ComposeUp(ctx context.Context, services []string) error {
	args := append([]string{"up", "-d"}, services...)
	return dockerx.Compose(ctx, d.dir, args...)
}

// CreateObject ждёт готовности platform-api, а затем заводит объект.
func (d *liveUpDeps) CreateObject(ctx context.Context, name, owner string) (string, error) {
	if err := waitHealthy(ctx, d.hc, d.cfg.Ports.API); err != nil {
		return "", err
	}
	return d.world.CreateObject(ctx, name, owner)
}

func (d *liveUpDeps) BuildPush(ctx context.Context, ref string) (string, error) {
	return dockerx.BuildPush(ctx, d.cfg.Spacecraft.Context, d.cfg.Spacecraft.Dockerfile, ref)
}

// WriteCompose перерендеривает compose с реальным object_id и проверяет, что
// его достаточно для второй фазы — RequireObjectIDForPhaseTwo зовётся здесь,
// непосредственно перед тем, как upWith поднимет вторую фазу.
func (d *liveUpDeps) WriteCompose(objectID, digest, ref string) error {
	p := stack.Params{
		Cfg:              d.cfg,
		ObjectID:         objectID,
		APIToken:         d.secrets.APIToken,
		GatewayToken:     d.secrets.GatewayToken,
		Namespace:        namespace,
		ServiceAccount:   ServiceAccountFor(objectID),
		SpacecraftImage:  ref,
		SpacecraftDigest: digest,
	}
	if err := stack.RequireObjectIDForPhaseTwo(p); err != nil {
		return err
	}
	return stack.Write(d.dir, p)
}

// waitHealthy опрашивает GET /healthz platform-api до 90 секунд.
//
// Ждём platform-api, а не полагаемся на depends_on: compose считает службу
// поднятой по запуску процесса, а миграции и подключение к БД занимают
// секунды. POST /objects в это окно вернёт отказ соединения, и up развалится
// на ровном месте.
func waitHealthy(ctx context.Context, hc *http.Client, port int) error {
	const (
		timeout = 90 * time.Second
		poll    = time.Second
	)
	url := fmt.Sprintf("http://localhost:%d/healthz", port)
	deadline := time.Now().Add(timeout)

	var lastErr error
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return fmt.Errorf("собрать запрос GET /healthz: %w", err)
		}
		resp, err := hc.Do(req)
		if err != nil {
			lastErr = err
		} else {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
			lastErr = fmt.Errorf("неожиданный статус %d", resp.StatusCode)
		}

		if time.Now().After(deadline) {
			return fmt.Errorf("platform-api не ответил на /healthz за %s: %w", timeout, lastErr)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(poll):
		}
	}
}
