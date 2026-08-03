package issuer_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/khorost-space/space-lab/internal/issuer"
)

// getContext — http.Get с явным контекстом: линтер noctx требует
// NewRequestWithContext вместо голого http.Get даже в тестах, а дублировать
// эту обвязку в каждом тесте незачем.
func getContext(t *testing.T, url string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("собрать запрос: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("запрос %s: %v", url, err)
	}
	return resp
}

func newIssuer(t *testing.T) *issuer.Issuer {
	t.Helper()
	key, err := issuer.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	return issuer.New("http://dev-issuer:18084", key)
}

// TestDiscoveryDeclaresItsOwnURL: go-oidc сверяет поле issuer в discovery с
// тем URL, по которому пришёл. Расхождение — отказ старта Gateway с
// сообщением про oidc, из которого причина не видна.
func TestDiscoveryDeclaresItsOwnURL(t *testing.T) {
	srv := httptest.NewServer(newIssuer(t).Handler())
	defer srv.Close()

	resp := getContext(t, srv.URL+"/.well-known/openid-configuration")
	defer func() { _ = resp.Body.Close() }()

	var doc struct {
		Issuer  string   `json:"issuer"`
		JWKSURI string   `json:"jwks_uri"`
		Algs    []string `json:"id_token_signing_alg_values_supported"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		t.Fatalf("разобрать discovery: %v", err)
	}
	if doc.Issuer != "http://dev-issuer:18084" {
		t.Errorf("issuer = %q, ожидался объявленный при сборке", doc.Issuer)
	}
	if doc.JWKSURI != "http://dev-issuer:18084/keys" {
		t.Errorf("jwks_uri = %q", doc.JWKSURI)
	}
	if len(doc.Algs) == 0 || doc.Algs[0] != "RS256" {
		t.Errorf("алгоритмы подписи = %v, ожидался RS256", doc.Algs)
	}
}

// TestJWKSServesSigningKey: ключ из JWKS обязан проверять выпущенный токен —
// иначе Gateway отвергнет каждый сигнал, а полигон будет выглядеть рабочим.
func TestJWKSServesSigningKey(t *testing.T) {
	iss := newIssuer(t)
	srv := httptest.NewServer(iss.Handler())
	defer srv.Close()

	raw, err := iss.Mint("space-lab", "object-1", 10*time.Minute, time.Now())
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}

	resp := getContext(t, srv.URL+"/keys")
	defer func() { _ = resp.Body.Close() }()

	var set struct {
		Keys []map[string]any `json:"keys"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&set); err != nil {
		t.Fatalf("разобрать jwks: %v", err)
	}
	if len(set.Keys) != 1 {
		t.Fatalf("ключей в jwks: %d, ожидался 1", len(set.Keys))
	}
	if set.Keys[0]["kid"] != issuer.KeyID {
		t.Errorf("kid = %v, ожидался %q", set.Keys[0]["kid"], issuer.KeyID)
	}
	if _, err := jwt.ParseSigned(raw, []jose.SignatureAlgorithm{jose.RS256}); err != nil {
		t.Errorf("выпущенный токен не разбирается: %v", err)
	}
}

// TestMintClaimsMatchProjectedToken: форма claims копирует проецированный
// SA-токен (ADR-0018). Расхождение в имени claim даёт отказ Gateway
// «отображение не найдено», по которому причину не найти.
func TestMintClaimsMatchProjectedToken(t *testing.T) {
	iss := newIssuer(t)
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)

	raw, err := iss.Mint("space-lab", "object-vega", 600*time.Second, now)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	tok, err := jwt.ParseSigned(raw, []jose.SignatureAlgorithm{jose.RS256})
	if err != nil {
		t.Fatalf("разобрать: %v", err)
	}

	var std jwt.Claims
	var k8s struct {
		Kubernetes struct {
			Namespace      string `json:"namespace"`
			ServiceAccount struct {
				Name string `json:"name"`
			} `json:"serviceaccount"`
		} `json:"kubernetes.io"`
	}
	if err := tok.UnsafeClaimsWithoutVerification(&std, &k8s); err != nil {
		t.Fatalf("прочитать claims: %v", err)
	}

	if std.Subject != "system:serviceaccount:space-lab:object-vega" {
		t.Errorf("sub = %q", std.Subject)
	}
	if len(std.Audience) != 1 || std.Audience[0] != issuer.Audience {
		t.Errorf("aud = %v, ожидался %q", std.Audience, issuer.Audience)
	}
	if k8s.Kubernetes.Namespace != "space-lab" || k8s.Kubernetes.ServiceAccount.Name != "object-vega" {
		t.Errorf("claim kubernetes.io неполон: %+v", k8s)
	}
	if got := std.Expiry.Time().UTC(); !got.Equal(now.Add(600 * time.Second)) {
		t.Errorf("exp = %v, ожидался now+600s", got)
	}
	if got := std.NotBefore.Time().UTC(); got.After(now) {
		t.Errorf("nbf = %v позже now: токен невалиден в момент выпуска", got)
	}
}
