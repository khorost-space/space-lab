// Package project — конфигурация проекта студента: что поднимать и на каких
// портах.
//
// Файл space-lab.yaml лежит в корне проекта и КОММИТИТСЯ: это настройки
// упражнения, а не машины. Сгенерированное и секретное живёт в .space-lab/ и
// в репозиторий не попадает.
package project

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	// ConfigFile — имя файла конфигурации в корне проекта.
	ConfigFile = "space-lab.yaml"
	// StateDir — каталог сгенерированного состояния: compose, токены, ключи.
	StateDir = ".space-lab"
)

// Config — настройки полигона для одного проекта.
//
// Плоская структура без указателей: конфигурация сравнивается на равенство в
// тестах, и указатель сделал бы это сравнением адресов.
type Config struct {
	Object     Object     `yaml:"object"`
	Spacecraft Spacecraft `yaml:"spacecraft"`
	Platform   Platform   `yaml:"platform"`
	Ports      Ports      `yaml:"ports"`
}

// Object — как аппарат называется в мире.
type Object struct {
	Name  string `yaml:"name"`
	Owner string `yaml:"owner"`
}

// Spacecraft — где взять образ аппарата и где его пробы.
type Spacecraft struct {
	Context    string `yaml:"context"`
	Dockerfile string `yaml:"dockerfile"`
	// HealthPort — порт проб ВНУТРИ контейнера. Наружу он публикуется на
	// Ports.Health: занимать у студента фиксированный порт нельзя, у него на
	// машине может слушать что угодно.
	HealthPort int `yaml:"health_port"`
}

// Platform — откуда брать образы платформы.
type Platform struct {
	Registry string `yaml:"registry"`
	// Version закрепляется, а не берётся latest: latest означает, что
	// вчерашний зелёный check сегодня необъясним.
	Version string `yaml:"version"`
	// IssuerVersion — версия образа локального dev-issuer, отдельная от
	// Version. Issuer собирается из самого space-lab, а не из платформы, и
	// версионируется своим циклом: тегировать его версией платформы значило
	// бы, что при первом же расхождении версий полигон потянет
	// несуществующий образ.
	IssuerVersion string `yaml:"issuer_version"`
}

// Ports — порты, публикуемые на машине студента.
type Ports struct {
	API      int `yaml:"api"`
	Gateway  int `yaml:"gateway"`
	Health   int `yaml:"health"`
	Registry int `yaml:"registry"`
	Issuer   int `yaml:"issuer"`
}

// Default собирает конфигурацию по умолчанию.
//
// Порты выбраны в диапазоне 18xxx, а не 8080/5000: у студента на машине уже
// что-то слушает привычные порты, и конфликт на первом же up читался бы как
// поломка полигона.
func Default(name string) Config {
	return Config{
		Object: Object{Name: name, Owner: "student"},
		Spacecraft: Spacecraft{
			Context:    ".",
			Dockerfile: "Dockerfile",
			HealthPort: 8080,
		},
		Platform: Platform{
			Registry: "ghcr.io/khorost-space",
			// 0.0.0-7b93a281 закреплён, а не latest — ровно по той причине,
			// которую называет докстринг Platform.Version: latest означает,
			// что вчерашний зелёный check сегодня необъясним. Тег выбран не
			// произвольно: именно в нём впервые появилась правка числа
			// реплик стрима NATS (KHOROST_NATS_REPLICAS в compose.tmpl), без
			// которой platform-worker не может создать стрим на
			// одноузловом NATS полигона и вообще не стартует.
			Version: "0.0.0-7b93a281",
			// latest здесь — НЕ то же послабление, что было бы у
			// Platform.Version. Образ issuer собирается из ЭТОГО же
			// репозитория (Dockerfile.issuer, job image в ci.yml), и latest
			// для него означает «версия, соответствующая этому исходнику
			// space-lab», а не «что угодно, что собралось последним у
			// соседей» — именно второе и есть настоящая опасность latest,
			// от которой закреплён Platform.Version.
			IssuerVersion: "latest",
		},
		Ports: Ports{API: 18080, Gateway: 18081, Health: 18082, Registry: 18083, Issuer: 18084},
	}
}

// Validate проверяет конфигурацию целиком.
func (c Config) Validate() error {
	if c.Object.Name == "" {
		return errors.New("object.name пусто: у объекта в мире должно быть имя")
	}
	if c.Object.Owner == "" {
		return errors.New("object.owner пусто")
	}
	if c.Spacecraft.Context == "" || c.Spacecraft.Dockerfile == "" {
		return errors.New("spacecraft.context и spacecraft.dockerfile обязательны")
	}
	if c.Spacecraft.HealthPort <= 0 || c.Spacecraft.HealthPort > 65535 {
		return fmt.Errorf("spacecraft.health_port = %d: порт вне диапазона", c.Spacecraft.HealthPort)
	}
	if c.Platform.Registry == "" || c.Platform.Version == "" || c.Platform.IssuerVersion == "" {
		return errors.New("platform.registry, platform.version и platform.issuer_version обязательны")
	}
	return c.validatePorts()
}

// validatePorts проверяет диапазон и уникальность публикуемых портов.
//
// Вынесено из Validate: карта имён и цикл сравнения — самостоятельный кусок,
// и Validate иначе перестаёт читаться одним взглядом.
func (c Config) validatePorts() error {
	seen := map[int]string{}
	for _, p := range []struct {
		name string
		port int
	}{
		{"ports.api", c.Ports.API},
		{"ports.gateway", c.Ports.Gateway},
		{"ports.health", c.Ports.Health},
		{"ports.registry", c.Ports.Registry},
		{"ports.issuer", c.Ports.Issuer},
	} {
		if p.port <= 0 || p.port > 65535 {
			return fmt.Errorf("%s = %d: порт вне диапазона", p.name, p.port)
		}
		if other, dup := seen[p.port]; dup {
			return fmt.Errorf("%s и %s заняли один порт %d", p.name, other, p.port)
		}
		seen[p.port] = p.name
	}
	return nil
}

// Load читает конфигурацию из каталога проекта.
func Load(dir string) (Config, error) {
	path := filepath.Join(dir, ConfigFile)
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Config{}, fmt.Errorf("%s не найден: выполните «space-lab init»", ConfigFile)
		}
		return Config{}, fmt.Errorf("прочитать %s: %w", ConfigFile, err)
	}
	var c Config
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	// KnownFields: опечатка в имени поля иначе молча даёт нулевое значение.
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		return Config{}, decodeError(err)
	}
	if err := c.Validate(); err != nil {
		return Config{}, fmt.Errorf("%s: %w", ConfigFile, err)
	}
	return c, nil
}

// configError — ошибка разбора конфигурации с русским текстом снаружи и
// исходной ошибкой yaml.v3 внутри.
//
// Обычное fmt.Errorf("%s: %w", msg, err) не годится: %w вставляет текст
// исходной ошибки в Error(), а она у yaml.v3 — английская и называет
// внутренний Go-тип (project.Config), студенту это ни о чём не говорит.
// Здесь Error() отдаёт только русский текст, а Unwrap сохраняет исходную
// ошибку для errors.Is/errors.As и отладки.
type configError struct {
	msg string
	err error
}

func (e *configError) Error() string { return e.msg }
func (e *configError) Unwrap() error { return e.err }

// unknownFieldRe вытаскивает номер строки и имя поля из сообщения вида
// «line 18: field неизвестное_поле not found in type project.Config»,
// которое отдаёт yaml.v3 при KnownFields(true).
var unknownFieldRe = regexp.MustCompile(`^line (\d+): field (.+) not found in type `)

// decodeError переводит ошибку yaml.Decoder в русский текст, называющий файл
// и, по возможности, конкретную причину — неизвестное поле или синтаксис.
func decodeError(err error) error {
	var typeErr *yaml.TypeError
	if errors.As(err, &typeErr) {
		return &configError{msg: fmt.Sprintf("%s: %s", ConfigFile, describeTypeError(typeErr)), err: err}
	}
	return &configError{
		msg: fmt.Sprintf("%s: не удалось разобрать YAML, проверьте синтаксис файла", ConfigFile),
		err: err,
	}
}

// describeTypeError переводит сообщения yaml.TypeError построчно; для
// неизвестных полей — с номером строки и именем поля, для прочих случаев —
// общей фразой без сырого английского текста библиотеки.
func describeTypeError(te *yaml.TypeError) string {
	parts := make([]string, 0, len(te.Errors))
	for _, e := range te.Errors {
		if m := unknownFieldRe.FindStringSubmatch(e); m != nil {
			parts = append(parts, fmt.Sprintf("неизвестное поле %q (строка %s)", m[2], m[1]))
			continue
		}
		parts = append(parts, "структура файла не совпадает с ожидаемой")
	}
	if len(parts) == 0 {
		return "структура файла не совпадает с ожидаемой"
	}
	return strings.Join(parts, "; ")
}

// Save записывает конфигурацию в каталог проекта.
func Save(dir string, c Config) error {
	raw, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("сериализовать конфигурацию: %w", err)
	}
	return os.WriteFile(filepath.Join(dir, ConfigFile), raw, 0o644)
}
