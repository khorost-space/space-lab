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
