package stack_test

import (
	"strings"
	"testing"

	"github.com/khorost-space/space-lab/internal/project"
	"github.com/khorost-space/space-lab/internal/stack"
	"gopkg.in/yaml.v3"
)

func params() stack.Params {
	return stack.Params{
		Cfg:              project.Default("vega-0"),
		ObjectID:         "019f7f3a-127c-7b25-a8d3-e049ffbba58f",
		APIToken:         "api-секрет",
		GatewayToken:     "gw-секрет",
		Namespace:        "space-lab",
		ServiceAccount:   "object-019f7f3a",
		SpacecraftImage:  "localhost:18083/spacecraft@sha256:" + strings.Repeat("a", 64),
		SpacecraftDigest: "sha256:" + strings.Repeat("a", 64),
	}
}

// TestRenderIsValidYAML: compose, который не разбирается, даёт отказ docker
// без указания строки — ловим здесь.
func TestRenderIsValidYAML(t *testing.T) {
	raw, err := stack.Render(params())
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("сгенерирован невалидный YAML: %v\n%s", err, raw)
	}
	services, ok := doc["services"].(map[string]any)
	if !ok {
		t.Fatal("в compose нет services")
	}
	for _, name := range []string{
		"postgres", "redis", "migrate", "platform-api", "platform-worker",
		"student-gateway", "dev-issuer", "registry", "spacecraft",
	} {
		if _, ok := services[name]; !ok {
			t.Errorf("нет службы %q", name)
		}
	}
}

// TestBindingCarriesObjectAndIssuer: отображение Gateway — пять полей через
// вертикальную черту (issuer|namespace|serviceaccount|object_id|environment).
// Ошибка здесь даёт «отображение не найдено» на каждом сигнале.
func TestBindingCarriesObjectAndIssuer(t *testing.T) {
	raw, err := stack.Render(params())
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	want := "http://dev-issuer:8081|space-lab|object-019f7f3a|019f7f3a-127c-7b25-a8d3-e049ffbba58f|local"
	if !strings.Contains(string(raw), want) {
		t.Errorf("в compose нет отображения %q", want)
	}
}

// TestGatewayAndAPIShareToken: platform-api принимает /heartbeats по
// KHOROST_GATEWAY_TOKEN, Gateway им же ходит. Расхождение даёт 401 на каждом
// сигнале при внешне исправном стеке.
func TestGatewayAndAPIShareToken(t *testing.T) {
	raw, err := stack.Render(params())
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if n := strings.Count(string(raw), "KHOROST_GATEWAY_TOKEN: gw-секрет"); n != 2 {
		t.Errorf("KHOROST_GATEWAY_TOKEN встречается %d раз, ожидалось 2 (api и gateway)", n)
	}
}

// TestShowcasePointsAtTheObject: витрина — источник наблюдаемого состояния и
// заявленного digest для status и check. Без KHOROST_SHOWCASE_OBJECT_ID её
// маршруты не монтируются вовсе.
func TestShowcasePointsAtTheObject(t *testing.T) {
	raw, err := stack.Render(params())
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(string(raw), "KHOROST_SHOWCASE_OBJECT_ID: 019f7f3a-127c-7b25-a8d3-e049ffbba58f") {
		t.Error("витрина не указывает на объект полигона")
	}
}

// TestSpacecraftRunsByDigest: центрально Argo раскатывает по digest. Запуск по
// подвижному тегу локально означал бы, что проверка совпадения digest
// проверяет не то же самое.
func TestSpacecraftRunsByDigest(t *testing.T) {
	raw, err := stack.Render(params())
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(string(raw), "@sha256:") {
		t.Error("аппарат запускается не по digest")
	}
}
