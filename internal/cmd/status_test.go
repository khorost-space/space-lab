package cmd_test

import (
	"strings"
	"testing"
	"time"

	"github.com/khorost-space/space-lab/internal/cmd"
	"github.com/khorost-space/space-lab/internal/worldapi"
)

// testVegaName — имя объекта, повторяющееся в нескольких тестах: goconst
// иначе просит вынести литерал в константу.
const testVegaName = "vega-0"

// TestFormatStatusShowsConditionAndVersion: это и есть «мир показывает
// online» из цели работы 1 — единственное место, где студент видит вердикт
// мира о своём аппарате.
func TestFormatStatusShowsConditionAndVersion(t *testing.T) {
	seen := time.Date(2026, 8, 3, 15, 36, 43, 0, time.UTC)
	got := cmd.FormatStatus(worldapi.View{
		Name: testVegaName, Condition: "online",
		LastSequence: 17, LastSeen: &seen, ServedVersion: "5fafbc183649",
	})
	for _, want := range []string{testVegaName, "online", "5fafbc183649", "17"} {
		if !strings.Contains(got, want) {
			t.Errorf("в выводе нет %q:\n%s", want, got)
		}
	}
}

// TestFormatStatusSaysNoSignalsInsteadOfZeroTime: nil last_seen честно
// означает «сигналов не было». Нулевое время выглядело бы как факт из первого
// века.
func TestFormatStatusSaysNoSignalsInsteadOfZeroTime(t *testing.T) {
	got := cmd.FormatStatus(worldapi.View{Name: testVegaName, Condition: "unknown"})
	if strings.Contains(got, "0001") {
		t.Errorf("нулевое время напечатано как факт:\n%s", got)
	}
	if !strings.Contains(got, "сигналов не было") {
		t.Errorf("отсутствие сигналов не названо:\n%s", got)
	}
}

// TestFormatStatusDoesNotPromiseOffline: состояния offline в платформе нет —
// реализованы unknown, online, signal_lost. Документы, обещающие offline,
// врут, и полигон не должен пополнять их список.
func TestFormatStatusDoesNotPromiseOffline(t *testing.T) {
	if strings.Contains(cmd.FormatStatus(worldapi.View{Condition: "signal_lost"}), "offline") {
		t.Error("вывод упоминает offline")
	}
}
