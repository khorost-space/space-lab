package cmd

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
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
// (фаза 1), затем объект в мире, затем локальный реестр образов, затем
// сборка и публикация образа аппарата в него, затем перезапись compose с
// реальным object_id и, наконец, фаза 2 (идентичность, Gateway, тот же
// реестр повторно — идемпотентно — и сам аппарат).
//
// Порядок пиннится тестами (TestUpOrdersPhases и соседние), а не
// комментарием: Gateway читает отображение из конфигурации ПРИ СТАРТЕ, а
// object_id выдаёт платформа — поднять Gateway раньше значит поднять его с
// пустым отображением. Реестр поднимается СВОИМ отдельным шагом до
// BuildPush по той же причине, что и объект: BuildPush пушит образ именно
// в него, а реестр в PhaseOne не входит (платформе он не нужен) и не может
// ждать PhaseTwo — она поднимается уже ПОСЛЕ BuildPush.
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

	// Реестр нужен раньше сборки: BuildPush пушит образ аппарата именно
	// туда. Без этого шага «docker push» бил бы по ещё не поднятому
	// контейнеру — «connection refused» вместо понятной ошибки об образе,
	// причём на КАЖДОМ подъёме, а не изредка.
	_, _ = fmt.Fprintln(stdout, "Поднимаем локальный реестр образов")
	if err = deps.ComposeUp(ctx, []string{"registry"}); err != nil {
		return fmt.Errorf("поднять реестр образов: %w", err)
	}

	// 127.0.0.1:<port>, а не localhost:<port> — снаружи, с машины студента,
	// куда docker build и docker push обращаются напрямую через
	// опубликованный порт реестра.
	//
	// Не опечатка и не то же самое: демон Docker включает реестр в
	// insecure-registries по умолчанию только для сети 127.0.0.0/8, а не
	// для ::1. Если «localhost» на машине резолвится сначала в IPv6
	// (обычный порядок в Windows), демон решает, что реестр «внешний», и
	// пытается говорить с ним HTTPS — с обычным HTTP-реестром это
	// зависает до таймаута на TLS-рукопожатии, а не падает сразу. Голый
	// IPv4-адрес убирает саму развилку.
	buildRef := fmt.Sprintf("127.0.0.1:%d/spacecraft:local", cfg.Ports.Registry)
	_, _ = fmt.Fprintln(stdout, "Собираем и публикуем образ аппарата")
	digest, err := deps.BuildPush(ctx, buildRef)
	if err != nil {
		return fmt.Errorf("собрать и опубликовать образ аппарата: %w", err)
	}

	// 127.0.0.1:<port> — тот же адрес, что и buildRef, а не «registry:5000»
	// по имени службы compose. Имя службы резолвится embedded DNS docker
	// compose только ВНУТРИ контейнеров уже поднятой сети — а образ тянет
	// сам демон Docker ДО того, как контейнер spacecraft создан, и его
	// путь пуллинга с DNS-именами проекта compose не пересекается вовсе:
	// «lookup registry: no such host». Прежняя версия этого комментария
	// утверждала обратное — то была ошибка, а не решение; digest у образа
	// один и тот же независимо от того, каким именем его вытянули.
	runImage := fmt.Sprintf("127.0.0.1:%d/spacecraft@%s", cfg.Ports.Registry, digest)
	if err := deps.WriteCompose(objectID, digest, runImage); err != nil {
		return fmt.Errorf("подготовить compose второй фазы: %w", err)
	}

	// platform-api поднят ещё первой фазой, но второй рендер дописал ему
	// KHOROST_SHOWCASE_OBJECT_ID — без витрины, которую эта переменная
	// монтирует, status и check читать нечего. compose up -d пересоздаёт
	// только службы с изменившейся секцией, поэтому лишнего перезапуска это
	// не даёт, но подхватить новую конфигурацию без этого шага он не может:
	// docker compose up -d на второй фазе службу platform-api вообще не
	// трогает, её нет в списке PhaseTwo.
	_, _ = fmt.Fprintln(stdout, "Перечитываем platform-api: витрина указывает на объект")
	if err := deps.ComposeUp(ctx, []string{"platform-api"}); err != nil {
		return fmt.Errorf("перезапустить platform-api с витриной: %w", err)
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
	if err := checkDockerfile(d.dir, d.cfg); err != nil {
		return "", err
	}
	return dockerx.BuildPush(ctx, d.cfg.Spacecraft.Context, d.cfg.Spacecraft.Dockerfile, ref)
}

// checkDockerfile проверяет, что файл сборки на месте, ДО запуска docker.
//
// Шаблон аппарата (khorost-space/spacecraft-template) поставляется без
// Dockerfile намеренно: многоступенчатая сборка и запуск не от root
// перечислены в приёмке работы 1, и дать их значило бы подарить требование.
// Поэтому первый up у КАЖДОГО студента упирается сюда, и сообщение обязано
// назвать файл и следующий шаг. Сырая ошибка docker («failed to read
// dockerfile») этого не говорит, а лезть за объяснением в исходники полигона
// студенту не с чем.
func checkDockerfile(dir string, cfg project.Config) error {
	path := filepath.Join(dir, cfg.Spacecraft.Context, cfg.Spacecraft.Dockerfile)
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf(
				"%s не найден: образ аппарата собирать не из чего — напишите %s "+
					"(многоступенчатая сборка, запуск не от root)",
				cfg.Spacecraft.Dockerfile, cfg.Spacecraft.Dockerfile)
		}
		return fmt.Errorf("прочитать %s: %w", cfg.Spacecraft.Dockerfile, err)
	}
	return nil
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
