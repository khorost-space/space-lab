package cmd

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/khorost-space/space-lab/internal/project"
)

// Secrets — локальные токены стека.
//
// Два разных токена, а не один: /objects и /heartbeats закрыты РАЗНЫМИ
// токенами намеренно (ADR-0018) — компрометация Gateway, разбирающего
// недоверенный ввод, не должна давать прав на объекты мира. Полигон
// воспроизводит это разделение, иначе он учит топологии, которой нет.
type Secrets struct {
	APIToken     string `json:"api_token"`
	GatewayToken string `json:"gateway_token"`
}

// secretsFile — путь к файлу токенов в каталоге состояния проекта.
func secretsFile(dir string) string {
	return filepath.Join(dir, project.StateDir, "secrets.json")
}

// LoadOrCreateSecrets читает токены из .space-lab/secrets.json, а при первом
// вызове генерирует их и сохраняет.
//
// Идемпотентность через файл, а не через параметр: up вызывается на каждом
// перезапуске стека, и токены обязаны пережить перезапуск — иначе уже
// поднятый Gateway ходит в platform-api со старым токеном и получает 401.
func LoadOrCreateSecrets(dir string) (Secrets, error) {
	path := secretsFile(dir)

	raw, err := os.ReadFile(path)
	switch {
	case err == nil:
		var s Secrets
		if unmarshalErr := json.Unmarshal(raw, &s); unmarshalErr != nil {
			return Secrets{}, fmt.Errorf("разобрать %s: %w", path, unmarshalErr)
		}
		return s, nil
	case !os.IsNotExist(err):
		return Secrets{}, fmt.Errorf("прочитать %s: %w", path, err)
	}

	apiToken, err := randomToken()
	if err != nil {
		return Secrets{}, fmt.Errorf("сгенерировать api-токен: %w", err)
	}
	gatewayToken, err := randomToken()
	if err != nil {
		return Secrets{}, fmt.Errorf("сгенерировать gateway-токен: %w", err)
	}
	s := Secrets{APIToken: apiToken, GatewayToken: gatewayToken}

	body, err := json.Marshal(s)
	if err != nil {
		return Secrets{}, fmt.Errorf("сериализовать токены: %w", err)
	}
	// 0o600: файл несёт оба секретных токена, а не только маркер наличия.
	if err := os.WriteFile(path, body, 0o600); err != nil {
		return Secrets{}, fmt.Errorf("записать %s: %w", path, err)
	}
	return s, nil
}

// randomToken генерирует 32 случайных байта и кодирует их для использования
// как значения заголовка Authorization.
func randomToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// ServiceAccountFor выводит имя ServiceAccount из object_id.
//
// Соглашение общее с кластером (object-<id>): значение фигурирует и в
// dev-issuer (KHOROST_TOKEN_SERVICEACCOUNT), и в отображении Gateway
// (KHOROST_GATEWAY_BINDINGS). Одна функция вместо повторения строки в
// нескольких местах — расхождение здесь даёт «отображение не найдено».
func ServiceAccountFor(objectID string) string {
	return "object-" + objectID
}
