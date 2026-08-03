// Package check — проверки полигона и их классы паритета (ADR-0020).
//
// Класс объявляет каждая проверка, и печатается он всегда. Проверка без
// класса не публикуется: зелёный результат без него читается как обещание,
// которого полигон дать не может.
package check

import (
	"fmt"
	"strings"
)

// Class — класс паритета: насколько локальный вердикт обязан совпасть с
// центральным.
type Class string

const (
	// Guaranteed — вердикт обязан совпасть локально и централизованно.
	// Расхождение здесь — дефект платформы, а не студента.
	Guaranteed Class = "guaranteed"
	// EnvironmentDependent — локально проверяется приближённо. Провал не
	// блокирует подачу: тайминг ноутбука студенту не подчиняется.
	EnvironmentDependent Class = "environment-dependent"
	// CentralOnly — локально не воспроизводится вовсе.
	CentralOnly Class = "central-only"
)

// Result — итог одной проверки.
//
// Skipped и Passed=false — не одно и то же: Skipped означает «не
// проверялось» (central-only или проверка не смогла собрать наблюдение), а
// Passed=false — «проверялось и провалилось». Сводка обязана различать эти
// два случая, иначе пропуск читается как провал или как успех — оба чтения
// врут.
type Result struct {
	Name    string
	Class   Class
	Passed  bool
	Skipped bool
	// Detail — что именно проверено и почему таков вердикт. Для Skipped —
	// причина, по которой проверка не выполнялась.
	Detail string
}

// Report — сводка проверок полигона.
type Report struct {
	Results []Result
}

// ExitCode возвращает 1, если есть непройденный результат класса Guaranteed
// без Skipped, иначе 0.
//
// EnvironmentDependent и CentralOnly код выхода не меняют: тайминг ноутбука
// студенту не подчиняется, а central-only полигон в принципе не проверяет —
// ни то, ни другое не повод отказать в подаче (ADR-0020).
func (r Report) ExitCode() int {
	for _, res := range r.Results {
		if res.Class == Guaranteed && !res.Skipped && !res.Passed {
			return 1
		}
	}
	return 0
}

// String печатает по строке на результат вида «[✓|✗|—] <имя> (<класс>) —
// <detail>», затем сводку «пройдено: N, провалено: M, не проверялось: K».
//
// Класс печатается у каждого результата без исключений: он и есть обещание
// платформы студенту, и снятое с одного результата обещание нельзя достроить
// по памяти при чтении отчёта. Пропущенное в число пройденных не входит —
// иначе сводка врёт в самую выгодную сторону.
func (r Report) String() string {
	var b strings.Builder
	var passed, failed, skipped int
	for _, res := range r.Results {
		mark := "✗"
		switch {
		case res.Skipped:
			mark = "—"
			skipped++
		case res.Passed:
			mark = "✓"
			passed++
		default:
			failed++
		}
		fmt.Fprintf(&b, "[%s] %s (%s)", mark, res.Name, res.Class)
		if res.Detail != "" {
			fmt.Fprintf(&b, " — %s", res.Detail)
		}
		b.WriteByte('\n')
	}
	fmt.Fprintf(&b, "пройдено: %d, провалено: %d, не проверялось: %d\n", passed, failed, skipped)
	return b.String()
}
