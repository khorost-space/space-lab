package dockerx

import (
	"errors"
	"strings"
	"testing"
)

// TestComposeArgsPinFile: без -f compose подхватит docker-compose.yaml из
// каталога проекта студента (у него он вполне может быть свой) и поднимет не
// тот стек.
func TestComposeArgsPinFile(t *testing.T) {
	got := composeArgs("/проект/.space-lab/docker-compose.yaml", "up", "-d")
	joined := strings.Join(got, " ")
	if !strings.HasPrefix(joined, "compose -f /проект/.space-lab/docker-compose.yaml ") {
		t.Errorf("файл compose не закреплён: %q", joined)
	}
	if !strings.HasSuffix(joined, " up -d") {
		t.Errorf("аргументы команды потеряны: %q", joined)
	}
}

// TestComposePSArgsShowsStoppedContainers: живой прогон уже ловил дефект
// здесь — StopAndWait сначала останавливает службу, а затем читает её код
// выхода через «docker compose ps». Без --all «ps» показывает только
// РАБОТАЮЩИЕ контейнеры, а только что остановленной службы в этом множестве
// уже нет — вывод всегда пуст, и GracefulShutdown вместо кода выхода видел
// «docker compose ps: пустой вывод» на каждой проверке, а не изредка.
func TestComposePSArgsShowsStoppedContainers(t *testing.T) {
	got := composePSArgs("/проект/.space-lab/docker-compose.yaml", "spacecraft")
	joined := strings.Join(got, " ")
	if !strings.Contains(joined, " --all ") {
		t.Errorf("нет --all — «ps» не увидит только что остановленную службу: %q", joined)
	}
	if !strings.HasSuffix(joined, " spacecraft") {
		t.Errorf("имя службы потеряно: %q", joined)
	}
}

// TestComposePSAllArgsHasNoServiceFilter: StackServicesDown обязан увидеть
// ВЕСЬ стек, а не одну службу — composePSArgs здесь не годится, у него
// последним аргументом всегда имя службы.
func TestComposePSAllArgsHasNoServiceFilter(t *testing.T) {
	got := composePSAllArgs("/проект/.space-lab/docker-compose.yaml")
	joined := strings.Join(got, " ")
	if !strings.Contains(joined, " --all ") {
		t.Errorf("нет --all — «ps» не увидит остановленные службы: %q", joined)
	}
	if strings.HasSuffix(joined, "spacecraft") || strings.HasSuffix(joined, "platform-api") {
		t.Errorf("аргументы содержат фильтр по службе, ожидался весь стек: %q", joined)
	}
}

// TestParseServicesDownFindsNonRunning: живой прогон уже терял упавшую
// службу платформы среди зелёного вывода check — только те строки, где
// State != running, обязаны попасть в список.
func TestParseServicesDownFindsNonRunning(t *testing.T) {
	out := `{"Service":"postgres","State":"running"}
{"Service":"platform-api","State":"exited"}
{"Service":"nats","State":"running"}
`
	got, err := parseServicesDown(out)
	if err != nil {
		t.Fatalf("parseServicesDown: %v", err)
	}
	if len(got) != 1 || !strings.Contains(got[0], "platform-api") || !strings.Contains(got[0], "exited") {
		t.Errorf("упавшие службы = %v, ожидался ровно platform-api (exited)", got)
	}
}

// TestParseServicesDownAllRunning: полностью здоровый стек не должен ничего
// возвращать — иначе check ложно откажется от полностью рабочего полигона.
func TestParseServicesDownAllRunning(t *testing.T) {
	out := `{"Service":"postgres","State":"running"}
{"Service":"redis","State":"running"}
`
	got, err := parseServicesDown(out)
	if err != nil {
		t.Fatalf("parseServicesDown: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("здоровый стек дал непустой список: %v", got)
	}
}

// TestParseServicesDownOneShotSuccessIsNotDown: находка живого прогона —
// migrate (stack.OneShot) обязана дойти до State="exited" по замыслу
// compose.tmpl (platform-api и platform-worker ждут её через
// service_completed_successfully), и State != "running" здесь НЕ отказ —
// отказ только у ненулевого ExitCode. До этой правки любой exited валил
// migrate вместе с реально упавшими службами, и check был красным на КАЖДОМ
// успешном up.
func TestParseServicesDownOneShotSuccessIsNotDown(t *testing.T) {
	out := `{"Service":"postgres","State":"running"}
{"Service":"migrate","State":"exited","ExitCode":0}
`
	got, err := parseServicesDown(out)
	if err != nil {
		t.Fatalf("parseServicesDown: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("успешно завершённая migrate зачтена упавшей: %v", got)
	}
}

// TestParseServicesDownOneShotFailureIsDown: та же migrate, но с ненулевым
// кодом выхода, — миграция схемы не накатилась, и это обязано провалить
// StackServicesDown так же громко, как упавшая долгоживущая служба.
func TestParseServicesDownOneShotFailureIsDown(t *testing.T) {
	out := `{"Service":"postgres","State":"running"}
{"Service":"migrate","State":"exited","ExitCode":1}
`
	got, err := parseServicesDown(out)
	if err != nil {
		t.Fatalf("parseServicesDown: %v", err)
	}
	if len(got) != 1 || !strings.Contains(got[0], "migrate") {
		t.Errorf("упавшая migrate не замечена: %v", got)
	}
}

// TestParseServicesDownLongRunningExitedIsDown: долгоживущая служба (включая
// spacecraft — его штатную остановку проверяет отдельная check.GracefulShutdown,
// которая идёт ПОСЛЕ StackServicesDown) обязана остаться под правилом
// «State != running — отказ», независимо от ExitCode: молча выйти ей нельзя
// ни при каком коде выхода. Регресс на правку одноразовых служб — до неё
// это уже было покрыто TestParseServicesDownFindsNonRunning, проверяю явно
// на spacecraft, чтобы не полагаться на то, что oneShotServices не задел
// долгоживущие по совпадению.
func TestParseServicesDownLongRunningExitedIsDown(t *testing.T) {
	out := `{"Service":"spacecraft","State":"exited","ExitCode":0}
`
	got, err := parseServicesDown(out)
	if err != nil {
		t.Fatalf("parseServicesDown: %v", err)
	}
	if len(got) != 1 || !strings.Contains(got[0], "spacecraft") {
		t.Errorf("вышедший spacecraft не замечен: %v", got)
	}
}

// TestParseServicesDownEmptyOutputMeansStackNotUp: находка после «space-lab
// down» — compose-файл остаётся на месте, но «docker compose ps --all»
// возвращает ПУСТОЙ вывод (контейнеров нет вовсе), и старый код читал его как
// «упавших служб нет» — check шёл дальше проверять аппарат, которого
// физически нет, и красным на пробах и сигналах расплачивался студент, а не
// полигон. Пустой вывод обязан провалиться отдельной ошибкой ДО того, как
// вызывающий код примет его за пустой список.
func TestParseServicesDownEmptyOutputMeansStackNotUp(t *testing.T) {
	_, err := parseServicesDown("")
	if err == nil {
		t.Fatal("пустой вывод docker compose ps принят за «упавших служб нет»")
	}
	if !errors.Is(err, ErrStackNotUp) {
		t.Errorf("ошибка = %v, ожидался ErrStackNotUp", err)
	}
	if !strings.Contains(err.Error(), "space-lab up") {
		t.Errorf("сообщение не подсказывает поднять стек: %v", err)
	}
}

// TestParseServicesDownWhitespaceOnlyOutputMeansStackNotUp: тот же случай, но
// вывод не буквально пуст, а состоит из пробелов/переводов строк — trim
// обязан свести его к тому же диагнозу.
func TestParseServicesDownWhitespaceOnlyOutputMeansStackNotUp(t *testing.T) {
	if _, err := parseServicesDown("\n  \n"); !errors.Is(err, ErrStackNotUp) {
		t.Errorf("ошибка = %v, ожидался ErrStackNotUp", err)
	}
}

// TestParseDigestTakesBareDigest: доставка платформы уже ловила этот дефект —
// crane пишет полную ссылку ref@sha256:…, а потребителю нужен голый digest.
// Тот же класс ошибки здесь дал бы digest_mismatch на каждом сигнале.
func TestParseDigestTakesBareDigest(t *testing.T) {
	out := `The push refers to repository [localhost:18083/spacecraft]
latest: digest: sha256:` + strings.Repeat("a", 64) + ` size: 1160`
	got, err := parseDigest(out)
	if err != nil {
		t.Fatalf("parseDigest: %v", err)
	}
	want := "sha256:" + strings.Repeat("a", 64)
	if got != want {
		t.Errorf("digest = %q, ожидался %q", got, want)
	}
}

// TestParseDigestFailsLoudlyWhenAbsent: digest, которого нет в выводе, нельзя
// подменить пустой строкой — аппарат заявил бы о себе пустоту, и мир принял
// бы её за версию.
func TestParseDigestFailsLoudlyWhenAbsent(t *testing.T) {
	if _, err := parseDigest("Pushed\nno digest here\n"); err == nil {
		t.Fatal("отсутствие digest принято за успех")
	}
}
