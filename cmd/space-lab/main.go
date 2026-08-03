// Command space-lab — локальный полигон аппарата (ADR-0008, профиль minimal).
package main

import (
	"fmt"
	"io"
	"os"
)

// run вынесен из main ради проверяемости: main не принимает аргументы и не
// возвращает код, поэтому разбор команд иначе не протестировать.
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return 2
	}
	switch args[0] {
	case "init", "up", "status", "check", "down":
		// Ошибка записи в stderr не проверяется: если он уже недоступен,
		// исправлять это здесь всё равно нечем — код возврата важнее.
		_, _ = fmt.Fprintf(stderr, "space-lab: команда %q ещё не реализована\n", args[0])
		return 1
	default:
		_, _ = fmt.Fprintf(stderr, "space-lab: неизвестная команда %q\n\n", args[0])
		usage(stderr)
		return 2
	}
}

func usage(w io.Writer) {
	_, _ = fmt.Fprint(w, `space-lab — локальный полигон аппарата

Команды:
  init     подготовить проект: space-lab.yaml и .space-lab/
  up       поднять мир и аппарат
  status   показать состояние аппарата в мире
  check    прогнать проверки и напечатать вердикт
  down     остановить и убрать стек
`)
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}
