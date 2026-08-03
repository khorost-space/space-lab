// Command space-lab — локальный полигон аппарата (ADR-0008, профиль minimal).
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/khorost-space/space-lab/internal/cmd"
)

// run вынесен из main ради проверяемости: main не принимает аргументы и не
// возвращает код, поэтому разбор команд иначе не протестировать.
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return 2
	}
	switch args[0] {
	case "init":
		return runInit(args[1:], stdout, stderr)
	case "up":
		return runUp(args[1:], stdout, stderr)
	case "down":
		return runDown(args[1:], stdout, stderr)
	case "status", "check":
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

// runInit разбирает флаги «init» и запускает cmd.Init в текущем каталоге.
//
// Имя объекта по умолчанию берётся из имени рабочего каталога: студент,
// который создал каталог vega-0/, не должен ещё раз печатать это имя во
// флаге.
func runInit(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(stderr)
	name := fs.String("name", "", "имя объекта в мире (по умолчанию — имя текущего каталога)")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	dir, err := os.Getwd()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "space-lab: узнать текущий каталог: %v\n", err)
		return 1
	}
	objectName := *name
	if objectName == "" {
		objectName = filepath.Base(dir)
	}

	if err := cmd.Init(dir, objectName, stdout); err != nil {
		_, _ = fmt.Fprintf(stderr, "space-lab: %v\n", err)
		return 1
	}
	return 0
}

// runUp разбирает флаги «up» (их нет) и запускает cmd.Up в текущем каталоге.
func runUp(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("up", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}

	dir, err := os.Getwd()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "space-lab: узнать текущий каталог: %v\n", err)
		return 1
	}
	if err := cmd.Up(context.Background(), dir, stdout); err != nil {
		_, _ = fmt.Fprintf(stderr, "space-lab: %v\n", err)
		return 1
	}
	return 0
}

// runDown разбирает флаги «down» (-purge) и запускает cmd.Down в текущем
// каталоге.
func runDown(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("down", flag.ContinueOnError)
	fs.SetOutput(stderr)
	purge := fs.Bool("purge", false, "удалить и тома (identity, postgres) — без него данные переживают down")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	dir, err := os.Getwd()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "space-lab: узнать текущий каталог: %v\n", err)
		return 1
	}
	if err := cmd.Down(context.Background(), dir, *purge, stdout); err != nil {
		_, _ = fmt.Fprintf(stderr, "space-lab: %v\n", err)
		return 1
	}
	return 0
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
