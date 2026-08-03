package cmd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/khorost-space/space-lab/internal/dockerx"
	"github.com/khorost-space/space-lab/internal/project"
	"github.com/khorost-space/space-lab/internal/worldapi"
)

// conditionOnline и testVegaName — литералы, повторяющиеся в нескольких
// тестах: goconst иначе просит вынести их в константу.
const (
	conditionOnline = "online"
	testVegaName    = "vega-0"
)

// withFastPolling подменяет observationWindow и pollInterval на малые
// значения на время теста и восстанавливает боевые по его завершении —
// иначе каждый тест observe/checkWith реально ждал бы до 45 секунд.
func withFastPolling(t *testing.T, window, poll time.Duration) {
	t.Helper()
	origWindow, origPoll := observationWindow, pollInterval
	observationWindow, pollInterval = window, poll
	t.Cleanup(func() { observationWindow, pollInterval = origWindow, origPoll })
}

// showcaseStep — одно значение витрины для showcaseStub. LastSeen НЕ
// задаётся заранее для сигналов, принятых ВО ВРЕМЯ теста (Signal=true):
// сервер проставляет его сам в момент ответа на запрос (см. handler) —
// заранее вычисленный offset от time.Now() гонится с реальным временем
// выполнения теста (setup httptest.Server, HTTP round-trip) и на медленной
// машине (WSL, CI) давал бы флаки, засчитывая честный сигнал как «пришедший
// до старта наблюдения». FixedSeen — исключение: он нужен ровно для сигнала,
// который обязан остаться в прошлом (TestObserveIgnoresSignalFromBeforeWindow).
type showcaseStep struct {
	Condition     string
	Sequence      int64
	Signal        bool
	FixedSeen     *time.Time
	ServedVersion string
}

// showcaseStub — фейковая витрина: отдаёт по одному значению из steps на
// каждый запрос GET /showcase/data, оставаясь на последнем после исчерпания
// списка. Потокобезопасна: observe и test читают счётчик из разных горутин.
type showcaseStub struct {
	mu      sync.Mutex
	steps   []showcaseStep
	i       int
	reqLog  *[]string
	reqName string
}

func (s *showcaseStub) handler(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	step := s.steps[min(s.i, len(s.steps)-1)]
	if s.i < len(s.steps)-1 {
		s.i++
	}
	if s.reqLog != nil {
		*s.reqLog = append(*s.reqLog, s.reqName)
	}
	s.mu.Unlock()

	seen := step.FixedSeen
	if step.Signal && seen == nil {
		now := time.Now()
		seen = &now
	}
	resp := struct {
		Name          string     `json:"name"`
		Condition     string     `json:"condition"`
		LastSequence  int64      `json:"last_sequence"`
		LastSeen      *time.Time `json:"last_seen"`
		ServedVersion string     `json:"served_version"`
	}{testVegaName, step.Condition, step.Sequence, seen, step.ServedVersion}
	_ = json.NewEncoder(w).Encode(resp)
}

func newShowcaseServer(t *testing.T, steps []showcaseStep) (*worldapi.Client, *showcaseStub) {
	t.Helper()
	stub := &showcaseStub{steps: steps}
	srv := httptest.NewServer(http.HandlerFunc(stub.handler))
	t.Cleanup(srv.Close)
	return worldapi.New(srv.URL, "", srv.Client()), stub
}

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	tm, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("разобрать время %q: %v", s, err)
	}
	return tm
}

// TestObserveIgnoresSignalFromBeforeWindow: находка 1 — аппарат, приславший
// один сигнал ДО старта check и с тех пор молчащий, не должен засчитываться
// как «наблюдение». Витрина на каждый опрос отдаёт ОДНО И ТО ЖЕ старое
// значение (LastSequence не меняется) — ровно то, что видел бы check у
// зависшего аппарата.
func TestObserveIgnoresSignalFromBeforeWindow(t *testing.T) {
	withFastPolling(t, 300*time.Millisecond, 100*time.Millisecond)
	old := mustTime(t, "2020-01-01T00:00:00Z")
	world, _ := newShowcaseServer(t, []showcaseStep{
		{Condition: conditionOnline, Sequence: 1, FixedSeen: &old},
	})

	seq, seen, last, err := observe(context.Background(), world)
	if err != nil {
		t.Fatalf("observe: %v", err)
	}
	if len(seq) != 0 || len(seen) != 0 {
		t.Errorf("старый сигнал засчитан как наблюдение: seq=%v seen=%v", seq, seen)
	}
	if last.LastSequence != 1 {
		t.Errorf("последний снимок витрины потерян: %+v", last)
	}
}

// TestObserveCountsSignalsAcceptedDuringWindow: сигналы, реально принятые
// ПОСЛЕ старта наблюдения, обязаны попасть в seq/seen — иначе почти любой
// работающий аппарат тоже проваливал бы SignalAccepted.
func TestObserveCountsSignalsAcceptedDuringWindow(t *testing.T) {
	// window/poll с широким запасом: у каждого сигнала не меньше секунды на
	// то, чтобы его успели опросить, — даже на медленной машине (WSL, CI).
	withFastPolling(t, 5*time.Second, 100*time.Millisecond)
	world, _ := newShowcaseServer(t, []showcaseStep{
		{Condition: "unknown"},
		{Condition: conditionOnline, Sequence: 1, Signal: true},
		{Condition: conditionOnline, Sequence: 2, Signal: true},
		{Condition: conditionOnline, Sequence: 3, Signal: true},
	})

	seq, seen, _, err := observe(context.Background(), world)
	if err != nil {
		t.Fatalf("observe: %v", err)
	}
	if len(seq) != 3 || len(seen) != 3 {
		t.Fatalf("наблюдений = %d, ожидалось 3: %v", len(seq), seq)
	}
	if seq[0] != 1 || seq[1] != 2 || seq[2] != 3 {
		t.Errorf("номера наблюдений = %v, ожидалось [1 2 3]", seq)
	}
}

// TestReadComposeSpacecraftReadsImageAndDigest: check читает образ аппарата
// и заявленный digest из ТОГО ЖЕ compose, что сгенерировал up — отдельного
// файла состояния для этого нет.
func TestReadComposeSpacecraftReadsImageAndDigest(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, project.StateDir), 0o700); err != nil {
		t.Fatalf("создать %s: %v", project.StateDir, err)
	}
	compose := `
services:
  spacecraft:
    image: 127.0.0.1:18083/spacecraft@sha256:` + strings.Repeat("a", 64) + `
    environment:
      KHOROST_RELEASE_DIGEST: sha256:` + strings.Repeat("a", 64) + `
`
	path := filepath.Join(dir, project.StateDir, "docker-compose.yaml")
	if err := os.WriteFile(path, []byte(compose), 0o600); err != nil {
		t.Fatalf("записать compose: %v", err)
	}

	spec, err := readComposeSpacecraft(dir)
	if err != nil {
		t.Fatalf("readComposeSpacecraft: %v", err)
	}
	if !strings.Contains(spec.Services.Spacecraft.Image, "spacecraft@sha256:") {
		t.Errorf("образ прочитан неверно: %q", spec.Services.Spacecraft.Image)
	}
	digest := spec.Services.Spacecraft.Environment["KHOROST_RELEASE_DIGEST"]
	if !strings.HasPrefix(digest, "sha256:") {
		t.Errorf("digest прочитан неверно: %q", digest)
	}
}

// TestReadComposeSpacecraftMissingFileNamesUpCommand: без up файла compose
// нет — сообщение обязано подсказать команду, а не молча дать пустой check.
func TestReadComposeSpacecraftMissingFileNamesUpCommand(t *testing.T) {
	_, err := readComposeSpacecraft(t.TempDir())
	if err == nil {
		t.Fatal("отсутствующий compose прочитан без ошибки")
	}
	if !strings.Contains(err.Error(), "space-lab up") {
		t.Errorf("сообщение не подсказывает up: %v", err)
	}
}

// fakeCheckDeps — заглушка checkDeps, пишущая имена вызовов в calls вместо
// исполнения Docker. Тот же приём, что recordingDeps в up_test.go.
type fakeCheckDeps struct {
	calls *[]string

	down    []string
	downErr error

	user    string
	userErr error

	stopExit    int
	stopElapsed time.Duration
	stopErr     error
}

func (d *fakeCheckDeps) StackServicesDown(context.Context) ([]string, error) {
	*d.calls = append(*d.calls, "stack-services-down")
	return d.down, d.downErr
}

func (d *fakeCheckDeps) ImageUser(_ context.Context, image string) (string, error) {
	*d.calls = append(*d.calls, "image-user:"+image)
	return d.user, d.userErr
}

func (d *fakeCheckDeps) StopAndWait(_ context.Context, service string, _ time.Duration) (int, time.Duration, error) {
	*d.calls = append(*d.calls, "stop-and-wait:"+service)
	return d.stopExit, d.stopElapsed, d.stopErr
}

// vegaSpec — минимальный composeSpacecraft для checkWith-тестов: образ
// non-root и digest, совпадающий с тем, что отдаёт витрина.
func vegaSpec(digest string) composeSpacecraft {
	var spec composeSpacecraft
	spec.Services.Spacecraft.Image = "127.0.0.1:18083/spacecraft@" + digest
	spec.Services.Spacecraft.Environment = map[string]string{"KHOROST_RELEASE_DIGEST": digest}
	return spec
}

// TestCheckWithStopsSpacecraftLast: находка 5 — StopAndWait обязан идти
// ПОСЛЕДНИМ внешним действием: он останавливает аппарат, и после него
// собрать остальные наблюдения (витрину, non-root) уже нельзя.
func TestCheckWithStopsSpacecraftLast(t *testing.T) {
	// window/poll с широким запасом (см. TestObserveCountsSignalsAcceptedDuringWindow) —
	// на медленной машине тесная граница засчитывала сигнал как «до старта».
	withFastPolling(t, 3*time.Second, 100*time.Millisecond)
	digest := "sha256:" + strings.Repeat("a", 64)
	var reqLog []string
	world, stub := newShowcaseServer(t, []showcaseStep{
		// Первый шаг — baseline (ДО старта наблюдения): сигналов ещё не
		// было. Остальные появляются уже ВО ВРЕМЯ окна — их и обязан
		// засчитать observe. Три сигнала, а не один: SignalAccepted теперь
		// требует не менее expectedSignals (3) за окно, и с одним эта проверка
		// сама проваливалась бы, маскируя то, что тест на самом деле проверяет
		// (порядок вызовов, а не SignalAccepted).
		{Condition: "unknown"},
		{Condition: conditionOnline, Sequence: 1, Signal: true, ServedVersion: strings.Repeat("a", 12)},
		{Condition: conditionOnline, Sequence: 2, Signal: true, ServedVersion: strings.Repeat("a", 12)},
		{Condition: conditionOnline, Sequence: 3, Signal: true, ServedVersion: strings.Repeat("a", 12)},
	})
	stub.reqLog = &reqLog
	stub.reqName = "showcase"

	healthSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	t.Cleanup(healthSrv.Close)

	var calls []string
	deps := &fakeCheckDeps{calls: &calls, user: "65532:65532"}

	var out strings.Builder
	err := checkWith(context.Background(), vegaSpec(digest), digest, world, healthSrv.URL, deps, &out)
	if err != nil {
		t.Fatalf("checkWith: %v\n%s", err, out.String())
	}

	stopIdx := -1
	for i, c := range calls {
		if c == "stop-and-wait:"+spacecraftService {
			stopIdx = i
		}
	}
	if stopIdx == -1 {
		t.Fatal("StopAndWait не вызван")
	}
	if stopIdx != len(calls)-1 {
		t.Errorf("StopAndWait вызван не последним: %v", calls)
	}
	if len(reqLog) == 0 {
		t.Error("витрина ни разу не опрошена ДО остановки")
	}
}

// TestCheckWithReportsStopFailureAsGuaranteedFailure: если dockerx.StopAndWait
// отказал (аппарат не удалось даже остановить), это обязано попасть в отчёт
// как непройденный guaranteed-результат, а не потеряться молча.
func TestCheckWithReportsStopFailureAsGuaranteedFailure(t *testing.T) {
	withFastPolling(t, 300*time.Millisecond, 100*time.Millisecond)
	digest := "sha256:" + strings.Repeat("a", 64)
	world, _ := newShowcaseServer(t, []showcaseStep{
		{Condition: conditionOnline, Sequence: 1, Signal: true, ServedVersion: strings.Repeat("a", 12)},
	})
	healthSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	t.Cleanup(healthSrv.Close)

	var calls []string
	deps := &fakeCheckDeps{calls: &calls, user: "65532:65532", stopErr: context.DeadlineExceeded}

	var out strings.Builder
	err := checkWith(context.Background(), vegaSpec(digest), digest, world, healthSrv.URL, deps, &out)
	if err == nil {
		t.Fatal("отказ остановки аппарата не провалил check")
	}
	if !strings.Contains(out.String(), "остановить аппарат") {
		t.Errorf("причина отказа остановки не названа в отчёте:\n%s", out.String())
	}
}

// TestCheckWithFailsLoudlyWhenStackServiceIsDown: находка 2 — упавшая служба
// платформы обрывает check раньше остальных проверок, громко и без притворства,
// что это обычная проверка класса паритета аппарата.
func TestCheckWithFailsLoudlyWhenStackServiceIsDown(t *testing.T) {
	var calls []string
	deps := &fakeCheckDeps{calls: &calls, down: []string{"platform-api (exited)"}}

	// Витрина намеренно не поднята (nil-клиент вызвал бы панику при
	// обращении) — StackServicesDown обязан оборвать checkWith раньше, чем
	// он вообще дойдёт до observe.
	err := checkWith(context.Background(), composeSpacecraft{}, "", nil, "", deps, discardWriter{})
	if err == nil {
		t.Fatal("упавшая служба стека не провалила check")
	}
	if !strings.Contains(err.Error(), "platform-api") {
		t.Errorf("виновная служба не названа: %v", err)
	}
	if len(calls) != 1 || calls[0] != "stack-services-down" {
		t.Errorf("check пошёл дальше StackServicesDown: %v", calls)
	}
}

// TestCheckWithFailsLoudlyWhenStackIsNotUp: находка после «space-lab down» —
// compose-файл остаётся, но docker compose ps ничего не возвращает
// (dockerx.StackServicesDown отвечает dockerx.ErrStackNotUp вместо пустого
// списка, см. dockerx_test.go). checkWith обязан оборвать проверку тут же,
// как и при явно упавшей службе (TestCheckWithFailsLoudlyWhenStackServiceIsDown),
// а не пойти дальше опрашивать витрину и пробы аппарата, которого нет.
func TestCheckWithFailsLoudlyWhenStackIsNotUp(t *testing.T) {
	var calls []string
	deps := &fakeCheckDeps{calls: &calls, downErr: dockerx.ErrStackNotUp}

	// Витрина намеренно не поднята (nil-клиент вызвал бы панику при
	// обращении) — StackServicesDown обязан оборвать checkWith раньше, чем
	// он вообще дойдёт до observe.
	err := checkWith(context.Background(), composeSpacecraft{}, "", nil, "", deps, discardWriter{})
	if err == nil {
		t.Fatal("неподнятый стек не провалил check")
	}
	if !strings.Contains(err.Error(), "space-lab up") {
		t.Errorf("отказ не подсказывает поднять стек: %v", err)
	}
	if len(calls) != 1 || calls[0] != "stack-services-down" {
		t.Errorf("check пошёл дальше StackServicesDown: %v", calls)
	}
}

// discardWriter — io.Writer, отбрасывающий всё; io.Discard подошёл бы тоже,
// но собственный тип явно показывает, что вывод в тесте не нужен.
type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }
