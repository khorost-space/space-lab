package check_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/khorost-space/space-lab/internal/check"
	"github.com/khorost-space/space-lab/internal/worldapi"
)

// conditionOnline — литерал витрины, повторяющийся в нескольких тестах:
// goconst иначе просит вынести его в константу.
const conditionOnline = "online"

// TestDigestMatchesComparesShortForm: витрина отдаёт первые 12 символов
// digest без префикса алгоритма — полный наружу не отдаётся. Сравнивать надо
// ровно то, что доступно, и назвать это в тексте результата.
func TestDigestMatchesComparesShortForm(t *testing.T) {
	digest := "sha256:5fafbc1836491ca9e39257db28bde64f2536e12e8f671f0b6f99fe7d8585ca6b"
	got := check.DigestMatches(worldapi.View{ServedVersion: "5fafbc183649"}, digest)
	if !got.Passed {
		t.Errorf("совпадающий digest не зачтён: %+v", got)
	}
	if got.Class != check.Guaranteed {
		t.Errorf("класс = %q", got.Class)
	}
}

// TestDigestMismatchIsStudentFailure: аппарат заявил не то, что развёрнуто, —
// это цель работы 1 (корректная инъекция конфигурации), а не сбой полигона.
func TestDigestMismatchIsStudentFailure(t *testing.T) {
	digest := "sha256:" + strings.Repeat("a", 64)
	if check.DigestMatches(worldapi.View{ServedVersion: "5fafbc183649"}, digest).Passed {
		t.Error("расхождение digest зачтено")
	}
}

// TestSignalAcceptedRequiresSignalsInWindow: аппарат, приславший один
// heartbeat и зависший ДО старта наблюдения, оставляет condition="online"
// сколь угодно долго (порог сноса — минута) — судить нужно по сигналам,
// увиденным ЗА ОКНО наблюдения (seq), а не по текущему снимку витрины.
func TestSignalAcceptedRequiresSignalsInWindow(t *testing.T) {
	// Витрина всё ещё "online" от старого сигнала, но за окно наблюдения
	// ничего не пришло — ровно сценарий замолчавшего аппарата.
	if check.SignalAccepted(worldapi.View{Condition: conditionOnline, LastSequence: 3}, nil, 3).Passed {
		t.Error("пустое окно наблюдения зачтено по condition=online")
	}
	if check.SignalAccepted(worldapi.View{Condition: "unknown"}, nil, 3).Passed {
		t.Error("отсутствие сигналов зачтено")
	}
	if !check.SignalAccepted(worldapi.View{Condition: conditionOnline, LastSequence: 3}, []int64{1, 2, 3}, 3).Passed {
		t.Error("три сигнала за окно наблюдения при пороге 3 не зачтены")
	}
}

// TestSignalAcceptedFailsOnSingleSignalBelowMin: находка — аппарат, приславший
// РОВНО ОДИН сигнал за окно наблюдения и зависший, обязан провалить проверку,
// а не пройти её по принципу «хотя бы один». При рабочей каденции 15 с за
// 45-секундное окно ожидается около трёх сигналов (min=3, как реально
// передаёт cmd.Check через expectedSignals) — один сигнал ниже этого порога
// центральная сборка засчитывает как отказ, и локальный check обязан
// расходиться с ней ровно так же, как ADR-0020 требует для guaranteed-класса.
func TestSignalAcceptedFailsOnSingleSignalBelowMin(t *testing.T) {
	got := check.SignalAccepted(worldapi.View{Condition: conditionOnline, LastSequence: 1}, []int64{1}, 3)
	if got.Passed {
		t.Errorf("один сигнал за окно наблюдения при пороге 3 зачтён пройденным: %+v", got)
	}
	if got.Class != check.Guaranteed {
		t.Errorf("класс = %q, ожидался guaranteed", got.Class)
	}
}

// TestSequenceMonotonic: не возрастающий sequence на связи мир игнорирует
// идемпотентно, но для студента это дефект его кода.
func TestSequenceMonotonic(t *testing.T) {
	if !check.SequenceMonotonic([]int64{1, 2, 3}).Passed {
		t.Error("возрастающая последовательность не зачтена")
	}
	if check.SequenceMonotonic([]int64{3, 2}).Passed {
		t.Error("убывающая последовательность зачтена")
	}
}

// TestSequenceMonotonicSkipsOnFewerThanTwoObservations: аппарат, приславший
// один сигнал за окно наблюдения и замолчавший, не должен получать «✓» по
// монотонности — судить попросту не из чего. Skip, а не Passed=true и не
// провал (см. комментарий у SequenceMonotonic).
func TestSequenceMonotonicSkipsOnFewerThanTwoObservations(t *testing.T) {
	for _, seq := range [][]int64{nil, {1}} {
		got := check.SequenceMonotonic(seq)
		if !got.Skipped {
			t.Errorf("SequenceMonotonic(%v) = %+v, ожидался Skipped", seq, got)
		}
		if got.Passed {
			t.Errorf("SequenceMonotonic(%v) зачтён пройденным при нехватке наблюдений", seq)
		}
		if got.Class != check.Guaranteed {
			t.Errorf("класс = %q", got.Class)
		}
	}
}

// TestNonRootRejectsRootAndEmpty: пустой User в конфигурации образа означает
// root — Dockerfile просто не сказал обратного.
func TestNonRootRejectsRootAndEmpty(t *testing.T) {
	for _, user := range []string{"", "root", "0", "0:0"} {
		if check.NonRoot(user).Passed {
			t.Errorf("User=%q зачтён как non-root", user)
		}
	}
	if !check.NonRoot("65532:65532").Passed {
		t.Error("числовой uid не зачтён")
	}
}

// TestNonRootRejectsRootByAnySegment: root узнаётся по ЛЮБОМУ сегменту —
// именем root или числом 0, — а не только по строке целиком. Проверка
// класса guaranteed: образ, реально работающий от root, обязан провалить её
// локально ровно так же, как централизованно, а «root:root», «root:0» и
// «0:root» — такой же root, как голое «root».
func TestNonRootRejectsRootByAnySegment(t *testing.T) {
	for _, user := range []string{"root:root", "root:0", "0:root", ":root", "root:"} {
		if check.NonRoot(user).Passed {
			t.Errorf("User=%q (root по сегменту) зачтён как non-root", user)
		}
	}
}

// TestNonRootFailsClosedOnUnparseableForm: форму, которую нельзя разобрать
// (больше двух сегментов), проверка трактует в строгую сторону — как
// провал, а не как пройденную: обратное умолчание тихо пропустило бы ровно
// то, что эта проверка обязана ловить.
func TestNonRootFailsClosedOnUnparseableForm(t *testing.T) {
	if check.NonRoot("65532:65532:extra").Passed {
		t.Error("неразбираемая форма User зачтена как non-root")
	}
}

// TestNonRootAcceptsBareUID: форма без группы, не совпадающая с root/0, —
// обычный некорневой пользователь.
func TestNonRootAcceptsBareUID(t *testing.T) {
	if !check.NonRoot("65532").Passed {
		t.Error("голый некорневой uid не зачтён")
	}
}

// TestCadenceIsEnvironmentDependent: проверка зависит от тайминга и
// планировщика, а условие класса guaranteed требует независимости от них.
func TestCadenceIsEnvironmentDependent(t *testing.T) {
	base := time.Now()
	seen := []time.Time{base, base.Add(15 * time.Second), base.Add(30 * time.Second)}
	got := check.Cadence(seen)
	if got.Class != check.EnvironmentDependent {
		t.Errorf("класс = %q, ожидался environment-dependent", got.Class)
	}
	if !got.Passed {
		t.Errorf("ровная каденция 15 с не зачтена: %+v", got)
	}
	if check.Cadence([]time.Time{base, base.Add(40 * time.Second)}).Passed {
		t.Error("интервал 40 с зачтён как каденция 15 с")
	}
}

// TestReproducibleBuildIsSkippedCentralOnly: повторная сборка того же коммита
// на бете дала ДРУГОЙ digest — воспроизводимости нет даже у сборки самой
// платформы, и обещать её локально было бы неправдой.
func TestReproducibleBuildIsSkippedCentralOnly(t *testing.T) {
	got := check.ReproducibleBuild()
	if !got.Skipped || got.Class != check.CentralOnly {
		t.Errorf("проверка воспроизводимости: %+v", got)
	}
	if got.Detail == "" {
		t.Error("причина пропуска не названа")
	}
}

// TestGateChecksAreCentralOnlyAndNamed: четыре пункта qualification gate
// «Первого сигнала» (ADR-0020), которые полигон не выполняет вовсе, обязаны
// быть названы вслух как пропущенные central-only, а не молча отсутствовать
// в отчёте.
func TestGateChecksAreCentralOnlyAndNamed(t *testing.T) {
	for _, got := range []check.Result{
		check.ManifestSchemaConformance(),
		check.BuildFromSHA(),
		check.NoSecretsInImage(),
		check.ExactDigestInDeployment(),
	} {
		if !got.Skipped || got.Class != check.CentralOnly {
			t.Errorf("%q: %+v, ожидался Skipped central-only", got.Name, got)
		}
		if got.Detail == "" {
			t.Errorf("%q: причина пропуска не названа", got.Name)
		}
	}
}

// TestGracefulShutdownWantsCleanExitInTime: аппарат, убитый по таймауту,
// теряет незавершённую работу при каждом раскате.
func TestGracefulShutdownWantsCleanExitInTime(t *testing.T) {
	if !check.GracefulShutdown(0, 2*time.Second, 10*time.Second).Passed {
		t.Error("чистое завершение за 2 с не зачтено")
	}
	if check.GracefulShutdown(137, 10*time.Second, 10*time.Second).Passed {
		t.Error("убийство по таймауту (137) зачтено")
	}
}

// TestHealthRequiresBothProbesOK: readinessProbe платформы живёт ровно тем
// же путём, что и здесь — расхождение обязано быть дефектом аппарата, а не
// полигона.
func TestHealthRequiresBothProbesOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	got := check.Health(t.Context(), srv.URL)
	if !got.Passed {
		t.Errorf("оба probe вернули 200, но результат не зачтён: %+v", got)
	}
	if got.Class != check.Guaranteed {
		t.Errorf("класс = %q", got.Class)
	}
}

// TestHealthFailsWhenReadyzNotOK: /healthz может быть готов раньше /readyz —
// зачитывать успех по одному из двух путей нельзя.
func TestHealthFailsWhenReadyzNotOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/readyz" {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	if check.Health(t.Context(), srv.URL).Passed {
		t.Error("readyz вернул не-200, но результат зачтён успешным")
	}
}
