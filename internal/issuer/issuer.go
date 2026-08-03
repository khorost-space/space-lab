// Package issuer — локальный OIDC-issuer полигона.
//
// Выпускает токен той же формы, что kubelet проецирует в под: ADR-0018
// требует, чтобы код аппарата не различал локальный и кластерный запуск.
// Совпадают путь файла, audience, имена claims и алгоритм подписи.
package issuer

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

const (
	// Audience — то, что проверяет Gateway (ADR-0018). Не настраивается:
	// значение фиксировано контрактом, и «настраиваемый» audience означал бы,
	// что полигон умеет выпускать токен, который центр не примет.
	Audience = "khorost-student-gateway"
	// KeyID различает ключи в JWKS. Ключ один, но заголовок kid обязателен
	// для выбора ключа проверяющей стороной.
	KeyID = "space-lab-1"

	sigAlg = "RS256"
)

// Issuer выпускает и отдаёт ключи.
type Issuer struct {
	url string
	key *rsa.PrivateKey
}

// GenerateKey создаёт ключ подписи. 2048 бит: тот же размер, что у dev-issuer
// платформы, и достаточный для локального полигона.
func GenerateKey() (*rsa.PrivateKey, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("сгенерировать ключ подписи: %w", err)
	}
	return key, nil
}

// New собирает issuer. url — адрес, по которому issuer доступен ИЗ Gateway:
// discovery обязан объявить ровно его, иначе go-oidc откажет.
func New(url string, key *rsa.PrivateKey) *Issuer {
	return &Issuer{url: url, key: key}
}

// Handler отдаёт discovery, JWKS и пробу.
func (i *Issuer) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{
			"issuer":                                i.url,
			"jwks_uri":                              i.url + "/keys",
			"id_token_signing_alg_values_supported": []string{sigAlg},
			"response_types_supported":              []string{"id_token"},
			"subject_types_supported":               []string{"public"},
		})
	})
	mux.HandleFunc("GET /keys", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{
			Key:       i.key.Public(),
			KeyID:     KeyID,
			Algorithm: sigAlg,
			Use:       "sig",
		}}})
	})
	// Проба нужна compose: Gateway обязан стартовать ПОСЛЕ того, как discovery
	// отвечает. Иначе NewVerifier не найдёт issuer и Gateway упадёт — намеренно
	// громко, но по причине гонки старта, а не конфигурации.
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	return mux
}

// Mint чеканит токен для пары (namespace, serviceAccount).
//
// now приходит аргументом, а не берётся из time.Now: срок жизни токена — то,
// что проверяется тестом, и подстановка часов снаружи делает это возможным
// без ожидания в реальном времени.
func (i *Issuer) Mint(namespace, serviceAccount string, ttl time.Duration, now time.Time) (string, error) {
	sig, err := jose.NewSigner(
		jose.SigningKey{Algorithm: jose.RS256, Key: jose.JSONWebKey{Key: i.key, KeyID: KeyID, Algorithm: sigAlg}},
		(&jose.SignerOptions{}).WithType("JWT"),
	)
	if err != nil {
		return "", fmt.Errorf("создать подписанта: %w", err)
	}

	std := jwt.Claims{
		Issuer:   i.url,
		Subject:  "system:serviceaccount:" + namespace + ":" + serviceAccount,
		Audience: jwt.Audience{Audience},
		Expiry:   jwt.NewNumericDate(now.Add(ttl)),
		// nbf на минуту назад: часы контейнера issuer и контейнера Gateway
		// расходятся, и токен, валидный «с этой секунды», отвергается первой же
		// проверкой на машине с отставшими часами.
		NotBefore: jwt.NewNumericDate(now.Add(-time.Minute)),
		IssuedAt:  jwt.NewNumericDate(now),
	}
	k8s := map[string]any{
		"kubernetes.io": map[string]any{
			"namespace":      namespace,
			"serviceaccount": map[string]any{"name": serviceAccount},
		},
	}
	raw, err := jwt.Signed(sig).Claims(std).Claims(k8s).Serialize()
	if err != nil {
		return "", fmt.Errorf("подписать токен: %w", err)
	}
	return raw, nil
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
