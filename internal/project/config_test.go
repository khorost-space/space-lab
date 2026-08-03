package project_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/khorost-space/space-lab/internal/project"
)

// appendLine дописывает строку в space-lab.yaml проекта в dir — так тест
// TestLoadRejectsUnknownField вносит опечатку в уже валидный файл, не
// собирая YAML руками.
func appendLine(t *testing.T, dir, line string) {
	t.Helper()
	f, err := os.OpenFile(filepath.Join(dir, project.ConfigFile), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("открыть %s: %v", project.ConfigFile, err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.WriteString(line + "\n"); err != nil {
		t.Fatalf("дописать %s: %v", project.ConfigFile, err)
	}
}

// TestDefaultIsValid: умолчания обязаны проходить собственную валидацию —
// иначе первый же init оставит студента с неисправным файлом.
func TestDefaultIsValid(t *testing.T) {
	if err := project.Default("vega-0").Validate(); err != nil {
		t.Fatalf("умолчания не проходят валидацию: %v", err)
	}
}

// TestSaveLoadRoundTrip: записанное читается обратно без потерь.
func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	want := project.Default("vega-0")
	want.Ports.API = 18080

	if err := project.Save(dir, want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := project.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got != want {
		t.Errorf("прочитано %+v, записано %+v", got, want)
	}
}

// TestValidateRejectsDuplicatePorts: два сервиса на одном порту дают отказ
// docker compose посреди подъёма, и сообщение там про занятый порт, а не про
// опечатку в конфигурации. Ловим раньше и по-русски.
func TestValidateRejectsDuplicatePorts(t *testing.T) {
	c := project.Default("vega-0")
	c.Ports.Gateway = c.Ports.API
	if err := c.Validate(); err == nil {
		t.Fatal("совпадающие порты приняты")
	}
}

// TestValidateRejectsEmptyName: объект без имени завести нельзя — platform-api
// отвергнет POST /objects, но уже после подъёма половины стека.
func TestValidateRejectsEmptyName(t *testing.T) {
	c := project.Default("vega-0")
	c.Object.Name = ""
	if err := c.Validate(); err == nil {
		t.Fatal("пустое имя объекта принято")
	}
}

// TestLoadMissingFileNamesFileAndCommand: сообщение обязано называть файл и
// команду, которой он создаётся.
func TestLoadMissingFileNamesFileAndCommand(t *testing.T) {
	_, err := project.Load(t.TempDir())
	if err == nil {
		t.Fatal("отсутствующий файл прочитан без ошибки")
	}
	got := err.Error()
	if !strings.Contains(got, project.ConfigFile) || !strings.Contains(got, "space-lab init") {
		t.Errorf("сообщение не помогает: %q", got)
	}
}

// TestLoadRejectsUnknownField: опечатка в имени поля иначе молча даёт нулевое
// значение, и студент видит не «неизвестное поле», а необъяснимое поведение.
func TestLoadRejectsUnknownField(t *testing.T) {
	dir := t.TempDir()
	if err := project.Save(dir, project.Default("vega-0")); err != nil {
		t.Fatalf("Save: %v", err)
	}
	appendLine(t, dir, "неизвестное_поле: 1")
	if _, err := project.Load(dir); err == nil {
		t.Fatal("неизвестное поле принято")
	}
}

// TestLoadUnknownFieldMessageIsRussian: сообщение обязано называть файл и
// объяснять причину по-русски, а не отдавать сырой английский текст yaml.v3
// с внутренним именем Go-типа (project.Config), который студенту ни о чём не
// говорит.
func TestLoadUnknownFieldMessageIsRussian(t *testing.T) {
	dir := t.TempDir()
	if err := project.Save(dir, project.Default("vega-0")); err != nil {
		t.Fatalf("Save: %v", err)
	}
	appendLine(t, dir, "неизвестное_поле: 1")
	_, err := project.Load(dir)
	if err == nil {
		t.Fatal("неизвестное поле принято")
	}
	got := err.Error()
	if !strings.Contains(got, project.ConfigFile) {
		t.Errorf("в сообщении нет имени файла: %q", got)
	}
	if !strings.Contains(got, "неизвестное поле") {
		t.Errorf("в сообщении нет русского объяснения причины: %q", got)
	}
	if strings.Contains(got, "not found in type") || strings.Contains(got, "project.Config") {
		t.Errorf("в сообщении остался сырой текст библиотеки: %q", got)
	}
}
