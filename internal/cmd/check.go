package cmd

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/khorost-space/space-lab/internal/check"
	"github.com/khorost-space/space-lab/internal/dockerx"
	"github.com/khorost-space/space-lab/internal/project"
	"github.com/khorost-space/space-lab/internal/worldapi"
)

const (
	// expectedSignals — сколько сигналов работы 1 (каденция ~15 с) check
	// рассчитывает увидеть за observationWindow.
	expectedSignals = 3
	// shutdownTimeout — сколько check ждёт штатной остановки аппарата,
	// прежде чем Docker пришлёт SIGKILL.
	shutdownTimeout = 10 * time.Second
	// spacecraftService — имя службы аппарата в compose; так его знает
	// dockerx.StopAndWait.
	spacecraftService = "spacecraft"
)

// observationWindow и pollInterval — var, а не const: unit-тесты observe
// (internal/cmd/check_test.go) подменяют оба на малые значения, чтобы не
// ждать реальные 45 секунд на каждый прогон «go test». Боевые значения не
// меняются никогда, кроме как в тесте, который сохраняет и восстанавливает
// их сам.
var (
	// observationWindow — сколько check наблюдает за витриной. 45 с = 3 ×
	// 15 с (каденция работы 1) с запасом на разброс планировщика.
	observationWindow = 45 * time.Second
	// pollInterval — как часто check опрашивает витрину внутри окна
	// наблюдения. Мельче каденции сигнала: иначе отдельные сигналы можно
	// пропустить между опросами и получить меньше наблюдений, чем было
	// реально принято.
	pollInterval = time.Second
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

// checkDeps — внешние действия check поверх Docker: тот же приём, что и
// upDeps в up.go — StopAndWait обязан идти ПОСЛЕДНИМ и обязан честно
// сообщать об ошибке остановки, а StackServicesDown обязан оборвать check
// раньше остальных проверок. Проверить эти ветки живым Docker в юнит-тесте
// нельзя, поэтому они вынесены за интерфейс.
type checkDeps interface {
	// StackServicesDown возвращает имена служб стека, которые не в
	// состоянии running (см. dockerx.StackServicesDown), с их текущим
	// состоянием — чтобы отчёт называл виновника, а не абстрактный «стек не
	// поднят».
	StackServicesDown(ctx context.Context) ([]string, error)
	// ImageUser читает Config.User образа аппарата (dockerx.ImageUser).
	ImageUser(ctx context.Context, image string) (string, error)
	// StopAndWait останавливает службу аппарата и возвращает код выхода
	// (dockerx.StopAndWait).
	StopAndWait(ctx context.Context, service string, timeout time.Duration) (exitCode int, elapsed time.Duration, err error)
}

// liveCheckDeps — боевая реализация checkDeps поверх dockerx, с каталогом
// проекта, зафиксированным при создании.
type liveCheckDeps struct {
	dir string
}

func (d *liveCheckDeps) StackServicesDown(ctx context.Context) ([]string, error) {
	return dockerx.StackServicesDown(ctx, d.dir)
}

func (d *liveCheckDeps) ImageUser(ctx context.Context, image string) (string, error) {
	return dockerx.ImageUser(ctx, image)
}

func (d *liveCheckDeps) StopAndWait(ctx context.Context, service string, timeout time.Duration) (int, time.Duration, error) {
	return dockerx.StopAndWait(ctx, d.dir, service, timeout)
}

// observe собирает наблюдения витрины за окно observationWindow: номера и
// время сигналов, ПРИНЯТЫХ ИМЕННО ЗА ЭТО ОКНО, по одному значению на каждую
// смену last_sequence, и последнее прочитанное состояние витрины целиком.
//
// Опрос, а не одиночное чтение: SequenceMonotonic и Cadence проверяют
// поведение аппарата ВО ВРЕМЕНИ, а витрина отдаёт только текущий снимок.
//
// Первое чтение — baseline, а не наблюдение: аппарат, приславший сигнал ДО
// старта check и с тех пор молчащий, оставляет этот сигнал в витрине сколь
// угодно долго (порог сноса — минута), а старый код засчитывал его как
// «наблюдение №1» просто потому, что видел его впервые. baseline фиксирует
// номер и время ЭТОГО старого сигнала, и оба признака ниже — и номер, и
// время — обязаны быть строго новее его, прежде чем что-то попадёт в seq.
// Одного номера недостаточно: если сигнал пришёл ровно между стартом start
// и запросом baseline, номер уже мог смениться ДО того, как наблюдение
// официально началось, — сравнение по времени страхует и этот случай.
func observe(ctx context.Context, world *worldapi.Client) (seq []int64, seen []time.Time, last worldapi.View, err error) {
	start := time.Now()
	deadline := start.Add(observationWindow)

	baseline, err := world.Showcase(ctx)
	if err != nil {
		return nil, nil, worldapi.View{}, fmt.Errorf("прочитать витрину: %w", err)
	}
	last = baseline
	lastSeq := baseline.LastSequence

	for {
		select {
		case <-ctx.Done():
			return seq, seen, last, ctx.Err()
		case <-time.After(pollInterval):
		}
		v, err := world.Showcase(ctx)
		if err != nil {
			return seq, seen, last, fmt.Errorf("прочитать витрину: %w", err)
		}
		last = v
		if v.LastSeen != nil && v.LastSequence != lastSeq && !v.LastSeen.Before(start) {
			seq = append(seq, v.LastSequence)
			seen = append(seen, *v.LastSeen)
			lastSeq = v.LastSequence
		}
		if len(seq) >= expectedSignals || time.Now().After(deadline) {
			return seq, seen, last, nil
		}
	}
}

// nonRootResult оборачивает check.NonRoot: если docker image inspect не
// смог прочитать пользователя образа, это провал самой проверки (класс
// guaranteed), а не молчаливый пропуск — Config.User в любом случае обязан
// быть читаем у образа, который уже запущен.
func nonRootResult(ctx context.Context, deps checkDeps, image string) check.Result {
	const name = "аппарат запускается не от root"
	user, err := deps.ImageUser(ctx, image)
	if err != nil {
		return check.Result{Name: name, Class: check.Guaranteed, Detail: fmt.Sprintf("docker image inspect: %v", err)}
	}
	return check.NonRoot(user)
}

// checkWith выполняет тело «space-lab check» поверх уже собранных
// зависимостей. Вынесено из Check тем же приёмом, что upWith из Up: тесты
// подставляют httptest.Server вместо витрины и фейковый checkDeps вместо
// Docker, вместо того чтобы поднимать настоящий стек.
//
// StackServicesDown идёт ПЕРВЫМ действием, раньше наблюдения за витриной и
// проб аппарата: упавшая служба ПЛАТФОРМЫ (а не аппарата) делает остальные
// проверки бессмысленными, а их зелёный результат на разваленном стеке —
// откровенно случайным. Отказ здесь не оформляется проверкой класса
// паритета и не попадает в Report: вины студента тут нет, а печатать такой
// отказ рядом с guaranteed/environment-dependent строками значило бы
// смешать дефект полигона с дефектом аппарата в одной сводке.
//
// check.GracefulShutdown идёт последней: она останавливает контейнер
// аппарата через checkDeps.StopAndWait, и после неё собрать остальные
// наблюдения уже нельзя.
func checkWith(
	ctx context.Context,
	spec composeSpacecraft,
	digest string,
	world *worldapi.Client,
	healthBase string,
	deps checkDeps,
	stdout io.Writer,
) error {
	_, _ = fmt.Fprintln(stdout, "Проверяем, что все службы стека подняты")
	down, err := deps.StackServicesDown(ctx)
	if err != nil {
		return fmt.Errorf("проверить состояние служб стека: %w", err)
	}
	if len(down) > 0 {
		return fmt.Errorf(
			"стек полигона поднят не целиком — это дефект полигона, а не аппарата: %s; выполните «space-lab up» заново",
			strings.Join(down, ", "),
		)
	}

	_, _ = fmt.Fprintf(stdout, "Наблюдаем за витриной (окно %s, до %d сигналов)\n", observationWindow, expectedSignals)
	seq, seen, last, err := observe(ctx, world)
	if err != nil {
		return err
	}

	results := []check.Result{
		check.Health(ctx, healthBase),
		// expectedSignals — тот же порог, которым observe() выше уже
		// ограничивает верхнюю границу наблюдений («не собирать больше
		// expectedSignals за окно»); здесь он же становится нижней границей
		// («не меньше expectedSignals»). Общая константа, а не отдельное
		// число: при смене observationWindow или cadenceTarget expectedSignals
		// пересчитывается один раз и обе границы остаются согласованы сами
		// собой — раздельные числа разъехались бы при первой же правке окна.
		check.SignalAccepted(last, seq, expectedSignals),
		check.DigestMatches(last, digest),
		check.SequenceMonotonic(seq),
		nonRootResult(ctx, deps, spec.Services.Spacecraft.Image),
		check.Cadence(seen),
		check.ReproducibleBuild(),
		check.ManifestSchemaConformance(),
		check.BuildFromSHA(),
		check.NoSecretsInImage(),
		check.ExactDigestInDeployment(),
	}

	_, _ = fmt.Fprintln(stdout, "Останавливаем аппарат: проверяем штатное завершение")
	exitCode, elapsed, stopErr := deps.StopAndWait(ctx, spacecraftService, shutdownTimeout)
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

// Check выполняет «space-lab check» в каталоге dir: читает конфигурацию и
// зафиксированный при up compose, собирает боевые зависимости (worldapi.Client,
// dockerx через liveCheckDeps) и вызывает checkWith.
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

	return checkWith(ctx, spec, digest, world, healthBase, &liveCheckDeps{dir: dir}, stdout)
}
