package check

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/khorost-space/space-lab/internal/worldapi"
)

// cadenceTarget и cadenceTolerance — ожидаемая каденция работы 1 и допуск.
//
// Допуск широкий намеренно: на ноутбуке под нагрузкой планировщик даёт
// разброс, и узкий допуск давал бы красный результат у исправного аппарата.
// Проверка ловит грубую ошибку (интервал в минуту или в секунду), а не
// измеряет точность — потому и класс environment-dependent.
const (
	cadenceTarget    = 15 * time.Second
	cadenceTolerance = 3 * time.Second
)

// digestShortLen — длина укороченной формы digest, которую отдаёт витрина
// (ServedVersion): первые 12 hex-символов без префикса алгоритма.
const digestShortLen = 12

// probeTimeout — таймаут одиночного HTTP-запроса пробы Health.
const probeTimeout = 5 * time.Second

// Health опрашивает GET /healthz и GET /readyz по адресу аппарата и зачитывает
// только код 200 у обоих: это ровно то, чем живёт readinessProbe платформы, и
// расхождение здесь — дефект аппарата, который увидит и центральная сборка.
func Health(ctx context.Context, baseURL string) Result {
	const name = "пробы здоровья аппарата"
	hc := &http.Client{Timeout: probeTimeout}
	for _, path := range []string{"/healthz", "/readyz"} {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+path, nil)
		if err != nil {
			return Result{Name: name, Class: Guaranteed, Detail: fmt.Sprintf("собрать запрос GET %s: %v", path, err)}
		}
		resp, err := hc.Do(req)
		if err != nil {
			return Result{Name: name, Class: Guaranteed, Detail: fmt.Sprintf("GET %s: %v", path, err)}
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return Result{
				Name: name, Class: Guaranteed,
				Detail: fmt.Sprintf("GET %s вернул %d, ожидался 200", path, resp.StatusCode),
			}
		}
	}
	return Result{Name: name, Class: Guaranteed, Passed: true, Detail: "healthz и readyz отвечают 200"}
}

// SignalAccepted проверяет, что мир принимает сигналы аппарата: unknown
// означает, что сигналов не было вовсе, и это провал, а не «пока не знаем».
func SignalAccepted(v worldapi.View) Result {
	const name = "сигналы приняты миром"
	passed := v.Condition == "online"
	detail := fmt.Sprintf("состояние витрины: %s, последний сигнал №%d", v.Condition, v.LastSequence)
	return Result{Name: name, Class: Guaranteed, Passed: passed, Detail: detail}
}

// DigestMatches сравнивает ServedVersion с первыми digestShortLen
// hex-символами digest после «:».
//
// Полный digest витрина наружу не отдаёт, поэтому сверка идёт по укороченной
// форме — и Detail говорит об этом прямо, а не делает вид, что сверен весь
// digest.
func DigestMatches(v worldapi.View, digest string) Result {
	const name = "заявленная версия совпадает с опубликованной"
	hex := digest
	if i := strings.IndexByte(digest, ':'); i >= 0 {
		hex = digest[i+1:]
	}
	short := hex
	if len(short) > digestShortLen {
		short = short[:digestShortLen]
	}
	passed := v.ServedVersion == short
	detail := fmt.Sprintf(
		"сверены первые %d символов digest (витрина полный digest не отдаёт): витрина %q, ожидалось %q",
		digestShortLen, v.ServedVersion, short,
	)
	return Result{Name: name, Class: Guaranteed, Passed: passed, Detail: detail}
}

// SequenceMonotonic проверяет строгое возрастание номеров сигналов: не
// возрастающий sequence на связи мир игнорирует идемпотентно, но для
// студента это дефект его кода.
func SequenceMonotonic(seq []int64) Result {
	const name = "номера сигналов возрастают"
	for i := 1; i < len(seq); i++ {
		if seq[i] <= seq[i-1] {
			return Result{
				Name: name, Class: Guaranteed,
				Detail: fmt.Sprintf("sequence не возрастает: %d после %d", seq[i], seq[i-1]),
			}
		}
	}
	return Result{Name: name, Class: Guaranteed, Passed: true, Detail: fmt.Sprintf("наблюдений: %d", len(seq))}
}

// NonRoot проверяет, что образ аппарата объявил непривилегированного
// пользователя: пустой User в конфигурации образа означает root — Dockerfile
// просто не сказал обратного.
func NonRoot(user string) Result {
	const name = "аппарат запускается не от root"
	trimmed := strings.TrimSpace(user)
	uid := trimmed
	if i := strings.IndexByte(trimmed, ':'); i >= 0 {
		uid = trimmed[:i]
	}
	passed := trimmed != "" && trimmed != "root" && uid != "0"
	detail := fmt.Sprintf("Config.User образа: %q", user)
	return Result{Name: name, Class: Guaranteed, Passed: passed, Detail: detail}
}

// Cadence проверяет, что интервалы между сигналами близки к 15 секундам
// работы 1, с широким допуском под разброс планировщика ноутбука.
//
// Class всегда EnvironmentDependent: проверка зависит от тайминга машины
// студента, а условие guaranteed требует независимости от него.
func Cadence(seen []time.Time) Result {
	const name = "каденция сигналов близка к 15 секундам"
	if len(seen) < 2 {
		return Result{
			Name: name, Class: EnvironmentDependent, Skipped: true,
			Detail: "меньше двух сигналов — интервал посчитать не из чего",
		}
	}
	minInterval, maxInterval := cadenceTarget, cadenceTarget
	for i := 1; i < len(seen); i++ {
		d := seen[i].Sub(seen[i-1])
		if d < minInterval {
			minInterval = d
		}
		if d > maxInterval {
			maxInterval = d
		}
	}
	passed := minInterval >= cadenceTarget-cadenceTolerance && maxInterval <= cadenceTarget+cadenceTolerance
	detail := fmt.Sprintf("интервалы от %s до %s, ожидалось %s ± %s",
		minInterval, maxInterval, cadenceTarget, cadenceTolerance)
	return Result{Name: name, Class: EnvironmentDependent, Passed: passed, Detail: detail}
}

// ReproducibleBuild всегда пропускается: повторная сборка того же коммита на
// бете дала ДРУГОЙ digest — воспроизводимости нет даже у сборки самой
// платформы, и обещать её локально было бы неправдой.
func ReproducibleBuild() Result {
	return Result{
		Name: "сборка воспроизводима побитово", Class: CentralOnly, Skipped: true,
		Detail: "не проверяется: повторная сборка того же коммита на бете уже давала другой digest",
	}
}

// GracefulShutdown проверяет, что аппарат завершился штатно и уложился в
// отведённый лимит: убитый по таймауту аппарат теряет незавершённую работу
// при каждом раскате.
func GracefulShutdown(exitCode int, elapsed, limit time.Duration) Result {
	const name = "аппарат завершается штатно и укладывается в лимит"
	passed := exitCode == 0 && elapsed <= limit
	detail := fmt.Sprintf("код выхода %d, остановка заняла %s из %s лимита", exitCode, elapsed, limit)
	return Result{Name: name, Class: Guaranteed, Passed: passed, Detail: detail}
}
