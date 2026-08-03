package dockerx

import (
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
