package cmd

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/khorost-space/space-lab/internal/check"
	"github.com/khorost-space/space-lab/internal/dockerx"
	"github.com/khorost-space/space-lab/internal/project"
	"github.com/khorost-space/space-lab/internal/worldapi"
)

const (
	// observationWindow — сколько check наблюдает за витриной, и
	// expectedSignals — сколько сигналов работы 1 (каденция ~15 с) он
	// рассчитывает увидеть за это время. 45 с = 3 × 15 с с запасом на
	// разброс планировщика.
	observationWindow = 45 * time.Second
	expectedSignals   = 3
	// pollInterval — как часто check опрашивает витрину внутри окна
	// наблюдения. Мельче каденции сигнала: иначе отдельные сигналы можно
	// пропустить между опросами и получить меньше наблюдений, чем было
	// реально принято.
	pollInterval = time.Second
	// shutdownTimeout — сколько check ждёт штатной остановки аппарата,
	// прежде чем Docker пришлёт SIGKILL.
	shutdownTimeout = 10 * time.Second
	// spacecraftService — имя службы аппарата в compose; так его знает
	// dockerx.StopAndWait.
	spacecraftService = "spacecraft"
)

// composeSpacecraft — минимальный срез сгенерированного docker-compose.yaml,
// нужный только check: заявленный образ аппарата (для dockerx.ImageUser) и
// зафиксированный при up digest (для check.DigestMatches). Полный
// stack.Params здесь не годится: токены и object_id — не про докер-образ,
// а секреты и state.Load их заново не читает.
type composeSpacecraft struct {
	Services struct {
		Spacecraft struct {
			Image       string            `yaml:"image"`
			Environment map[string]string `yaml:"environment"`
		} `yaml:"spacecraft"`
	} `yaml:"services"`
}

// readComposeSpacecraft читает срез compose аппарата из состояния проекта.
func readComposeSpacecraft(dir string) (composeSpacecraft, error) {
	path := filepath.Join(dir, project.StateDir, "docker-compose.yaml")
	raw, err := os.ReadFile(path)
	if err != nil {
		return composeSpacecraft{}, fmt.Errorf("прочитать %s: %w (выполните «space-lab up»)", path, err)
	}
	var spec composeSpacecraft
	if err := yaml.Unmarshal(raw, &spec); err != nil {
		return composeSpacecraft{}, fmt.Errorf("разобрать docker-compose.yaml: %w", err)
	}
	return spec, nil
}

// observe собирает наблюдения витрины за окно observationWindow: номера и
// время принятых сигналов, по одному значению на каждую смену
// last_sequence, и последнее прочитанное состояние витрины целиком.
//
// Опрос, а не одиночное чтение: SequenceMonotonic и Cadence проверяют
// поведение аппарата ВО ВРЕМЕНИ, а витрина отдаёт только текущий снимок.
func observe(ctx context.Context, world *worldapi.Client) (seq []int64, seen []time.Time, last worldapi.View, err error) {
	deadline := time.Now().Add(observationWindow)
	lastSeq := int64(-1)
	for {
		v, err := world.Showcase(ctx)
		if err != nil {
			return seq, seen, last, fmt.Errorf("прочитать витрину: %w", err)
		}
		last = v
		if v.LastSeen != nil && v.LastSequence != lastSeq {
			seq = append(seq, v.LastSequence)
			seen = append(seen, *v.LastSeen)
			lastSeq = v.LastSequence
		}
		if len(seq) >= expectedSignals || time.Now().After(deadline) {
			return seq, seen, last, nil
		}
		select {
		case <-ctx.Done():
			return seq, seen, last, ctx.Err()
		case <-time.After(pollInterval):
		}
	}
}

// nonRootResult оборачивает check.NonRoot: если docker image inspect не
// смог прочитать пользователя образа, это провал самой проверки (класс
// guaranteed), а не молчаливый пропуск — Config.User в любом случае обязан
// быть читаем у образа, который уже запущен.
func nonRootResult(ctx context.Context, image string) check.Result {
	const name = "аппарат запускается не от root"
	user, err := dockerx.ImageUser(ctx, image)
	if err != nil {
		return check.Result{Name: name, Class: check.Guaranteed, Detail: fmt.Sprintf("docker image inspect: %v", err)}
	}
	return check.NonRoot(user)
}

// Check выполняет «space-lab check» в каталоге dir: читает конфигурацию и
// зафиксированный при up compose, наблюдает за витриной, прогоняет проверки
// и печатает Report.String(). Возвращает ошибку, если ExitCode() != 0.
//
// check.GracefulShutdown идёт последней: она останавливает контейнер
// аппарата через dockerx.StopAndWait, и после неё собрать остальные
// наблюдения уже нельзя.
func Check(ctx context.Context, dir string, stdout io.Writer) error {
	cfg, err := project.Load(dir)
	if err != nil {
		return err
	}
	spec, err := readComposeSpacecraft(dir)
	if err != nil {
		return err
	}
	digest := spec.Services.Spacecraft.Environment["KHOROST_RELEASE_DIGEST"]

	hc := &http.Client{Timeout: 5 * time.Second}
	world := worldapi.New(fmt.Sprintf("http://localhost:%d", cfg.Ports.API), "", hc)
	healthBase := fmt.Sprintf("http://localhost:%d", cfg.Ports.Health)

	_, _ = fmt.Fprintf(stdout, "Наблюдаем за витриной (окно %s, до %d сигналов)\n", observationWindow, expectedSignals)
	seq, seen, last, err := observe(ctx, world)
	if err != nil {
		return err
	}

	results := []check.Result{
		check.Health(ctx, healthBase),
		check.SignalAccepted(last),
		check.DigestMatches(last, digest),
		check.SequenceMonotonic(seq),
		nonRootResult(ctx, spec.Services.Spacecraft.Image),
		check.Cadence(seen),
		check.ReproducibleBuild(),
	}

	_, _ = fmt.Fprintln(stdout, "Останавливаем аппарат: проверяем штатное завершение")
	exitCode, elapsed, stopErr := dockerx.StopAndWait(ctx, dir, spacecraftService, shutdownTimeout)
	if stopErr != nil {
		results = append(results, check.Result{
			Name: check.GracefulShutdownName, Class: check.Guaranteed,
			Detail: fmt.Sprintf("остановить аппарат: %v", stopErr),
		})
	} else {
		results = append(results, check.GracefulShutdown(exitCode, elapsed, shutdownTimeout))
	}

	report := check.Report{Results: results}
	_, _ = fmt.Fprint(stdout, report.String())
	if report.ExitCode() != 0 {
		return fmt.Errorf("проверки полигона провалены: есть непройденный результат класса %q", check.Guaranteed)
	}
	return nil
}
