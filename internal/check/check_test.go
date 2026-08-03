package check_test

import (
	"strings"
	"testing"

	"github.com/khorost-space/space-lab/internal/check"
)

// testProbesName — имя результата, повторяющееся в нескольких тестах:
// goconst иначе просит вынести литерал в константу.
const testProbesName = "пробы"

// TestExitCodeIgnoresEnvironmentDependent: тайминг ноутбука не должен
// блокировать подачу. Провал environment-dependent — предупреждение, а не
// отказ (ADR-0020).
func TestExitCodeIgnoresEnvironmentDependent(t *testing.T) {
	r := check.Report{Results: []check.Result{
		{Name: "каденция", Class: check.EnvironmentDependent, Passed: false},
		{Name: testProbesName, Class: check.Guaranteed, Passed: true},
	}}
	if r.ExitCode() != 0 {
		t.Errorf("код выхода = %d, ожидался 0", r.ExitCode())
	}
}

// TestExitCodeFailsOnGuaranteed: провал guaranteed — отказ.
func TestExitCodeFailsOnGuaranteed(t *testing.T) {
	r := check.Report{Results: []check.Result{
		{Name: testProbesName, Class: check.Guaranteed, Passed: false},
	}}
	if r.ExitCode() == 0 {
		t.Error("провал guaranteed дал успешный код выхода")
	}
}

// TestStringPrintsClassOfEveryResult: зелёный локальный результат никогда не
// утверждает прохождение того, что локально не проверялось (ADR-0020).
// Результат без класса рядом с ним читается как полноценный вердикт.
func TestStringPrintsClassOfEveryResult(t *testing.T) {
	r := check.Report{Results: []check.Result{
		{Name: testProbesName, Class: check.Guaranteed, Passed: true},
		{Name: "каденция", Class: check.EnvironmentDependent, Passed: true},
		{Name: "воспроизводимость сборки", Class: check.CentralOnly, Skipped: true,
			Detail: "локально не воспроизводится"},
	}}
	out := r.String()
	for _, want := range []string{"guaranteed", "environment-dependent", "central-only"} {
		if !strings.Contains(out, want) {
			t.Errorf("в отчёте нет класса %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "не проверялось") {
		t.Errorf("пропущенная проверка не названа вслух:\n%s", out)
	}
}

// TestSkippedIsNotPassed: пропущенная проверка не должна попадать в число
// пройденных — иначе сводка врёт в самую выгодную сторону.
func TestSkippedIsNotPassed(t *testing.T) {
	r := check.Report{Results: []check.Result{
		{Name: "воспроизводимость сборки", Class: check.CentralOnly, Skipped: true},
	}}
	if strings.Contains(r.String(), "пройдено: 1") {
		t.Errorf("пропущенное посчитано пройденным:\n%s", r.String())
	}
	if r.ExitCode() != 0 {
		t.Error("пропуск central-only дал ненулевой код")
	}
}
