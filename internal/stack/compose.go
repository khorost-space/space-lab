// Package stack — рендер docker compose для локального полигона: полный
// набор служб платформы, аппарата и вспомогательной инфраструктуры на одной
// машине студента.
package stack

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"text/template"

	"github.com/khorost-space/space-lab/internal/project"
)

// PhaseOne — службы первой фазы up: платформа без аппарата и Gateway.
var PhaseOne = []string{"postgres", "redis", "platform-api", "platform-worker"}

// PhaseTwo — службы второй фазы up: идентичность, Gateway, реестр и сам
// аппарат — то, что зависит от уже поднятой платформы.
var PhaseTwo = []string{"dev-issuer", "student-gateway", "registry", "spacecraft"}

// Params — данные, которых нет в project.Config, но без которых compose не
// собрать: они появляются только во время up (object_id, токены, digest
// собранного образа), а не хранятся в конфигурации проекта.
type Params struct {
	Cfg project.Config

	ObjectID       string
	APIToken       string
	GatewayToken   string
	Namespace      string
	ServiceAccount string

	// SpacecraftImage — полная ссылка, по которой аппарат запускается:
	// registry:5000/spacecraft@sha256:…
	SpacecraftImage string
	// SpacecraftDigest — голый sha256:<64hex>, который аппарат заявляет о
	// себе в KHOROST_RELEASE_DIGEST. Отдельно от SpacecraftImage: платформа
	// сверяет заявленный digest с тем, что реально запущено, и это два
	// разных по форме значения одного и того же digest.
	SpacecraftDigest string
}

//go:embed compose.tmpl
var composeTmpl string

var tmpl = template.Must(template.New("compose").Parse(composeTmpl))

// Render собирает docker-compose.yaml из Params.
func Render(p Params) ([]byte, error) {
	if err := validate(p); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, p); err != nil {
		return nil, fmt.Errorf("отрендерить compose: %w", err)
	}
	return buf.Bytes(), nil
}

// validate проверяет то, что шаблон молча проглотил бы пустым значением.
//
// KHOROST_SHOWCASE_OBJECT_ID в шаблоне защищён условием и просто исчезает
// при пустом ObjectID, а KHOROST_GATEWAY_BINDINGS подставляет его
// безусловно — без явной проверки здесь пустой ObjectID дал бы
// отображение с пустым полем и молчаливый отказ Gateway при первом
// сигнале, вместо понятной ошибки на этапе генерации.
func validate(p Params) error {
	if p.ObjectID == "" {
		return errors.New(
			"object_id не задан: службы второй фазы (dev-issuer, student-gateway, " +
				"spacecraft) не запустятся без него — сначала получите object_id через POST /objects",
		)
	}
	return nil
}

// Write кладёт docker-compose.yaml в каталог состояния проекта.
//
// Права 0o600, а не 0o644: в файле лежат оба секретных токена (API и
// Gateway) открытым текстом.
func Write(dir string, p Params) error {
	raw, err := Render(p)
	if err != nil {
		return err
	}
	path := filepath.Join(dir, project.StateDir, "docker-compose.yaml")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return fmt.Errorf("записать docker-compose.yaml: %w", err)
	}
	return nil
}
