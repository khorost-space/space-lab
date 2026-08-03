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

// GracefulShutdownName — имя результата GracefulShutdown.
//
// Экспортировано: cmd.Check собирает результат вручную, когда сам
// dockerx.StopAndWait отказал (аппарат не удалось даже остановить) — и без
// общей константы название разъехалось бы при первой же правке одного из
// двух мест.
const GracefulShutdownName = "аппарат завершается штатно и укладывается в лимит"

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

// SignalAccepted проверяет, что мир принял хотя бы один сигнал аппарата ЗА
// ОКНО НАБЛЮДЕНИЯ check — а не то, что витрина когда-либо видела сигнал.
//
// v.Condition для этого не годится: аппарат, приславший один heartbeat и
// зависший, оставляет condition="online" сколь угодно долго (порог сноса,
// signal_lost, наступает только через минуту молчания) — проверка на
// condition давала бы «пройдено» ровно в том случае, который обязана
// ловить. seq — сигналы, увиденные ИМЕННО во время наблюдения (см. observe
// в cmd.Check, который явно исключает сигнал, пришедший ДО его старта), и
// судить нужно по ним, а не по текущему снимку витрины.
func SignalAccepted(v worldapi.View, seq []int64) Result {
	const name = "сигналы приняты миром"
	passed := len(seq) > 0
	detail := fmt.Sprintf(
		"сигналов за окно наблюдения: %d, состояние витрины: %s, последний сигнал №%d",
		len(seq), v.Condition, v.LastSequence,
	)
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
	if len(seq) < 2 {
		// Меньше двух наблюдений — возрастание попросту не из чего
		// посчитать: последовательность из нуля или одного элемента
		// тривиально «монотонна», и печатать ✓ здесь означало бы
		// утверждать то, что не проверялось (та же ловушка, которую уже
		// решает соседняя Cadence при <2 сигналах). Skip, а не провал:
		// сам факт нехватки сигналов — забота SignalAccepted (он и ловит
		// аппарат, замолчавший в окне наблюдения), а эта проверка обязана
		// честно сказать «не из чего судить», а не приписать аппарату
		// несуществующий дефект монотонности.
		return Result{
			Name: name, Class: Guaranteed, Skipped: true,
			Detail: fmt.Sprintf("наблюдений за окно: %d — меньше двух, возрастание не из чего посчитать", len(seq)),
		}
	}
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
	detail := fmt.Sprintf("Config.User образа: %q", user)
	return Result{Name: name, Class: Guaranteed, Passed: nonRootPassed(user), Detail: detail}
}

// nonRootPassed разбирает Config.User образа (форма uid[:gid], сегменты
// могут быть числами или именами) и признаёт root по ЛЮБОМУ сегменту —
// числом 0 или именем root: «root:0», «0:root» и «root:root» — такой же
// root, как голое «root» или «0».
//
// Форму, которую разобрать нельзя (не 1 и не 2 сегмента, пустой сегмент),
// трактуем в строгую сторону — как провал, а не как пройденную проверку:
// обратное умолчание тихо пропустило бы ровно то, что проверка обязана
// ловить, а это проверка класса guaranteed — расхождение с центром здесь
// цена дефекта платформы, а не студента.
func nonRootPassed(user string) bool {
	trimmed := strings.TrimSpace(user)
	segments := strings.Split(trimmed, ":")
	if len(segments) > 2 {
		return false
	}
	for _, seg := range segments {
		if seg == "" || seg == "0" || seg == "root" {
			return false
		}
	}
	return true
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

// ManifestSchemaConformance, BuildFromSHA, NoSecretsInImage и
// ExactDigestInDeployment — central-only проверки qualification gate
// «Первого сигнала» (ADR-0020, «Применение к работам»): весь этот gate
// объявлен guaranteed, но полигон физически не может выполнить локально
// именно эти четыре — у него нет ни манифеста как отдельного артефакта,
// ни платформенного build-контейнера (образ собирает голый docker build
// студента), ни сканера слоёв образа, ни манифеста развёртывания. ADR-0020
// («Обязанности space-lab») требует явно перечислять такие проверки как
// пропущенные — иначе зелёный локальный check читается как обещание того,
// чего он не проверял. Единственная central-only проверка, которая была в
// отчёте до этих четырёх, — ReproducibleBuild — в списке ADR-0020 не
// числится вовсе; она остаётся отдельно и честно, но не заменяет собой
// пункты gate.

// ManifestSchemaConformance — central-only: соответствие manifest схеме
// проверяет центральная сборка, у полигона нет отдельного manifest-артефакта.
func ManifestSchemaConformance() Result {
	return Result{
		Name: "манифест соответствует схеме", Class: CentralOnly, Skipped: true,
		Detail: "не проверяется: у полигона нет manifest как отдельного артефакта — его собирает и валидирует центральная сборка",
	}
}

// BuildFromSHA — central-only: сборку платформой ИЗ ЗАЯВЛЕННОГО commit SHA
// проверяет центральный build-контейнер; полигон собирает образ голым
// docker build студента, а не тем же инструментом.
func BuildFromSHA() Result {
	return Result{
		Name: "образ собран платформой из заявленного SHA", Class: CentralOnly, Skipped: true,
		Detail: "не проверяется: полигон собирает образ обычным docker build, а не build-контейнером платформы из commit SHA",
	}
}

// NoSecretsInImage — central-only: полигон не сканирует слои собранного
// образа на секреты — эту проверку выполняет только центральная сборка.
func NoSecretsInImage() Result {
	return Result{
		Name: "в образе нет секретов", Class: CentralOnly, Skipped: true,
		Detail: "не проверяется: полигон не сканирует слои образа на секреты — это делает центральная сборка",
	}
}

// ExactDigestInDeployment — central-only: точный digest в манифесте
// развёртывания сверяет центральная сборка; полигон такого манифеста вообще
// не формирует, у него голый docker compose студента.
func ExactDigestInDeployment() Result {
	return Result{
		Name: "манифест развёртывания ссылается на точный digest", Class: CentralOnly, Skipped: true,
		Detail: "не проверяется: полигон не формирует манифест развёртывания — этим ведает центральная сборка",
	}
}

// GracefulShutdown проверяет, что аппарат завершился штатно и уложился в
// отведённый лимит: убитый по таймауту аппарат теряет незавершённую работу
// при каждом раскате.
func GracefulShutdown(exitCode int, elapsed, limit time.Duration) Result {
	passed := exitCode == 0 && elapsed <= limit
	detail := fmt.Sprintf("код выхода %d, остановка заняла %s из %s лимита", exitCode, elapsed, limit)
	return Result{Name: GracefulShutdownName, Class: Guaranteed, Passed: passed, Detail: detail}
}
