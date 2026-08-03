package main

import (
	"bytes"
	"strings"
	"testing"
)

// TestUnknownCommandFailsLoudly: неизвестная подкоманда — ненулевой код и
// подсказка. Молчаливый успех на опечатке хуже отказа: студент решит, что
// команда отработала.
func TestUnknownCommandFailsLoudly(t *testing.T) {
	var out, errOut bytes.Buffer
	code := run([]string{"поднять"}, &out, &errOut)
	if code == 0 {
		t.Error("неизвестная подкоманда завершилась успехом")
	}
	if !strings.Contains(errOut.String(), "поднять") {
		t.Errorf("в сообщении нет самой команды: %q", errOut.String())
	}
}

// TestNoArgsPrintsUsage: запуск без аргументов печатает список команд и
// возвращает ненулевой код.
func TestNoArgsPrintsUsage(t *testing.T) {
	var out, errOut bytes.Buffer
	code := run(nil, &out, &errOut)
	if code == 0 {
		t.Error("запуск без аргументов завершился успехом")
	}
	for _, want := range []string{"init", "up", "status", "check", "down"} {
		if !strings.Contains(errOut.String(), want) {
			t.Errorf("в подсказке нет команды %q", want)
		}
	}
}
