// Command space-lab-issuer — локальный OIDC-issuer полигона.
//
// Отдаёт discovery и JWKS, чеканит projected-токен на диск и перечёканивает
// его, пока живёт — так же, как kubelet ротирует токен, спроецированный в
// под. Форма токена и claims описаны в internal/issuer (ADR-0018).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/khorost-space/space-lab/internal/issuer"
)

const (
	defaultAddr      = ":8081"
	defaultTokenPath = "/var/run/khorost/identity/token"
	defaultNamespace = "space-lab"
	defaultTTL       = 600 * time.Second

	healthcheckTimeout = 2 * time.Second
	shutdownTimeout    = 5 * time.Second
)

// rotateEvery — период перезаписи токена.
//
// Половина TTL, а не сам TTL: аппарат читает файл в произвольный момент, и
// перезапись ровно по истечении означала бы окно, в котором в файле лежит
// уже недействительный токен. Kubelet по той же причине ротирует раньше
// срока.
func rotateEvery(ttl time.Duration) time.Duration { return ttl / 2 }

// config собран из переменных окружения: issuer живёт в compose-стеке, у
// которого нет иного способа передать параметры контейнеру на scratch.
type config struct {
	issuerURL      string
	addr           string
	tokenPath      string
	namespace      string
	serviceAccount string
	ttl            time.Duration
}

func loadConfig() (config, error) {
	cfg := config{
		issuerURL:      os.Getenv("KHOROST_ISSUER_URL"),
		addr:           envOr("KHOROST_ISSUER_ADDR", defaultAddr),
		tokenPath:      envOr("KHOROST_TOKEN_PATH", defaultTokenPath),
		namespace:      envOr("KHOROST_TOKEN_NAMESPACE", defaultNamespace),
		serviceAccount: os.Getenv("KHOROST_TOKEN_SERVICEACCOUNT"),
		ttl:            defaultTTL,
	}
	if cfg.issuerURL == "" {
		return config{}, errors.New("KHOROST_ISSUER_URL обязательна: адрес, по которому issuer виден Gateway")
	}
	if cfg.serviceAccount == "" {
		return config{}, errors.New("KHOROST_TOKEN_SERVICEACCOUNT обязательна: имя serviceaccount в claim'ах токена")
	}
	if raw := os.Getenv("KHOROST_TOKEN_TTL"); raw != "" {
		ttl, err := time.ParseDuration(raw)
		if err != nil {
			return config{}, fmt.Errorf("разобрать KHOROST_TOKEN_TTL: %w", err)
		}
		cfg.ttl = ttl
	}
	return cfg, nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func main() {
	healthcheck := flag.Bool("healthcheck", false, "проверить готовность локального issuer и выйти (проба compose)")
	flag.Parse()

	if *healthcheck {
		if err := runHealthcheck(envOr("KHOROST_ISSUER_ADDR", defaultAddr)); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "space-lab-issuer: проба не прошла: %v\n", err)
			os.Exit(1)
		}
		os.Exit(0)
	}

	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(log)

	if err := run(log); err != nil {
		log.Error("space-lab-issuer остановлен с ошибкой", "err", err)
		os.Exit(1)
	}
	log.Info("space-lab-issuer остановлен")
}

func run(log *slog.Logger) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	key, err := issuer.GenerateKey()
	if err != nil {
		return err
	}
	iss := issuer.New(cfg.issuerURL, key)

	log.Info("space-lab-issuer запускается",
		"issuer", cfg.issuerURL,
		"addr", cfg.addr,
		"tokenPath", cfg.tokenPath,
		"namespace", cfg.namespace,
		"serviceAccount", cfg.serviceAccount,
		"ttl", cfg.ttl,
	)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	srv := &http.Server{Addr: cfg.addr, Handler: iss.Handler()}

	// Оба обязательны: issuer без HTTP не отдаст discovery и JWKS, issuer без
	// ротации замолкнет через TTL после старта. Первый отказавший останавливает
	// второй через отмену общего контекста.
	errCh := make(chan error, 2)
	go func() { errCh <- serveHTTP(ctx, srv) }()
	go func() { errCh <- rotateLoop(ctx, iss, cfg, log) }()

	first := <-errCh
	stop()
	<-errCh
	return first
}

// serveHTTP запускает сервер и глушит его по отмене ctx graceful shutdown'ом.
func serveHTTP(ctx context.Context, srv *http.Server) error {
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("остановить HTTP-сервер issuer: %w", err)
		}
		return nil
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("HTTP-сервер issuer: %w", err)
	}
}

// rotateLoop чеканит токен немедленно (иначе аппарат, стартовавший раньше
// первого тика, упадёт на чтении отсутствующего файла), затем на каждом
// тике rotateEvery(ttl) — до отмены ctx.
func rotateLoop(ctx context.Context, iss *issuer.Issuer, cfg config, log *slog.Logger) error {
	mint := func() error {
		token, err := iss.Mint(cfg.namespace, cfg.serviceAccount, cfg.ttl, time.Now())
		if err != nil {
			return fmt.Errorf("выпустить токен: %w", err)
		}
		if err := issuer.WriteTokenFile(cfg.tokenPath, token); err != nil {
			return fmt.Errorf("записать файл токена: %w", err)
		}
		log.Info("токен перечеканен", "path", cfg.tokenPath, "ttl", cfg.ttl)
		return nil
	}

	if err := mint(); err != nil {
		return err
	}

	ticker := time.NewTicker(rotateEvery(cfg.ttl))
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := mint(); err != nil {
				return err
			}
		}
	}
}

// runHealthcheck — проба compose. Образ собран на scratch, curl в нём нет,
// поэтому пробу выполняет сам бинарник: одиночный запрос к /healthz и код
// выхода по ответу.
func runHealthcheck(addr string) error {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("разобрать KHOROST_ISSUER_ADDR: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), healthcheckTimeout)
	defer cancel()

	url := "http://" + net.JoinHostPort("127.0.0.1", port) + "/healthz"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("собрать запрос пробы: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("запрос пробы: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("проба вернула статус %d", resp.StatusCode)
	}
	return nil
}
