// token-injector is a tiny OAuth2 client_credentials reverse proxy.
//
// It is designed to run as a sidecar in front of an LLM provider API
// that requires a freshly-minted bearer token from an OAuth2 / OIDC
// token endpoint.
//
// The agent container talks plain HTTP to http://localhost:<LISTEN_PORT>
// and this binary:
//
//  1. Performs the OAuth2 `client_credentials` grant against TOKEN_URL
//     using TOKEN_INJECTOR_CLIENT_ID / TOKEN_INJECTOR_CLIENT_SECRET.
//  2. Caches the access_token until shortly before its `expires_in`.
//  3. Reverse-proxies every incoming request to TARGET_URL with the
//     `Authorization: Bearer <token>` header injected.
//
// Configuration (all via env vars):
//
//	TOKEN_INJECTOR_TARGET_URL      (required) upstream LLM API base URL
//	TOKEN_INJECTOR_TOKEN_URL       (required) OAuth2 token endpoint
//	TOKEN_INJECTOR_CLIENT_ID       (required) OAuth2 client id
//	TOKEN_INJECTOR_CLIENT_SECRET   (required) OAuth2 client secret
//	TOKEN_INJECTOR_LISTEN_PORT     (default 9101) localhost listen port
//	TOKEN_INJECTOR_SCOPE           (optional) space-separated scopes
//	TOKEN_INJECTOR_AUDIENCE        (optional) audience parameter
//	TOKEN_INJECTOR_TIMEOUT_SECONDS (default 60) upstream request timeout
//	TOKEN_INJECTOR_REFRESH_LEEWAY_SECONDS (default 30) refresh this many
//	                              seconds before the token would expire
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ----------------------------------------------------------------------------
// Config
// ----------------------------------------------------------------------------

type config struct {
	TargetURL     *url.URL
	TokenURL      string
	ClientID      string
	ClientSecret  string
	Scope         string
	Audience      string
	ListenPort    int
	Timeout       time.Duration
	RefreshLeeway time.Duration
}

func loadConfig() (*config, error) {
	target := os.Getenv("TOKEN_INJECTOR_TARGET_URL")
	tokenURL := os.Getenv("TOKEN_INJECTOR_TOKEN_URL")
	clientID := os.Getenv("TOKEN_INJECTOR_CLIENT_ID")
	clientSecret := os.Getenv("TOKEN_INJECTOR_CLIENT_SECRET")

	if target == "" {
		return nil, errors.New("TOKEN_INJECTOR_TARGET_URL is required")
	}
	if tokenURL == "" {
		return nil, errors.New("TOKEN_INJECTOR_TOKEN_URL is required")
	}
	if clientID == "" {
		return nil, errors.New("TOKEN_INJECTOR_CLIENT_ID is required")
	}
	if clientSecret == "" {
		return nil, errors.New("TOKEN_INJECTOR_CLIENT_SECRET is required")
	}

	tu, err := url.Parse(target)
	if err != nil {
		return nil, fmt.Errorf("invalid TOKEN_INJECTOR_TARGET_URL: %w", err)
	}
	if tu.Scheme == "" || tu.Host == "" {
		return nil, fmt.Errorf("TOKEN_INJECTOR_TARGET_URL must be absolute, got %q", target)
	}

	port := envIntDefault("TOKEN_INJECTOR_LISTEN_PORT", 9101)
	timeout := time.Duration(envIntDefault("TOKEN_INJECTOR_TIMEOUT_SECONDS", 60)) * time.Second
	leeway := time.Duration(envIntDefault("TOKEN_INJECTOR_REFRESH_LEEWAY_SECONDS", 30)) * time.Second

	return &config{
		TargetURL:     tu,
		TokenURL:      tokenURL,
		ClientID:      clientID,
		ClientSecret:  clientSecret,
		Scope:         os.Getenv("TOKEN_INJECTOR_SCOPE"),
		Audience:      os.Getenv("TOKEN_INJECTOR_AUDIENCE"),
		ListenPort:    port,
		Timeout:       timeout,
		RefreshLeeway: leeway,
	}, nil
}

func envIntDefault(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		log.Printf("token-injector: invalid %s=%q, using default %d", key, v, def)
		return def
	}
	return n
}

// ----------------------------------------------------------------------------
// Token cache (OAuth2 client_credentials)
// ----------------------------------------------------------------------------

type tokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
}

type tokenCache struct {
	cfg    *config
	client *http.Client

	mu        sync.Mutex
	token     string
	expiresAt time.Time
}

func newTokenCache(cfg *config) *tokenCache {
	return &tokenCache{
		cfg:    cfg,
		client: &http.Client{Timeout: cfg.Timeout},
	}
}

// Get returns a valid bearer token, refreshing if necessary. It serialises
// concurrent refreshes behind a mutex — for a single sidecar this is fine.
func (c *tokenCache) Get(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.token != "" && time.Until(c.expiresAt) > c.cfg.RefreshLeeway {
		return c.token, nil
	}

	form := url.Values{}
	form.Set("grant_type", "client_credentials")
	form.Set("client_id", c.cfg.ClientID)
	form.Set("client_secret", c.cfg.ClientSecret)
	if c.cfg.Scope != "" {
		form.Set("scope", c.cfg.Scope)
	}
	if c.cfg.Audience != "" {
		form.Set("audience", c.cfg.Audience)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.TokenURL,
		strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("token endpoint request: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("token endpoint returned %d: %s", resp.StatusCode, truncate(body, 512))
	}

	var tr tokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return "", fmt.Errorf("decode token response: %w", err)
	}
	if tr.AccessToken == "" {
		return "", errors.New("token endpoint returned empty access_token")
	}

	expiresIn := tr.ExpiresIn
	if expiresIn <= 0 {
		expiresIn = 300 // fallback: 5 min
	}
	c.token = tr.AccessToken
	c.expiresAt = time.Now().Add(time.Duration(expiresIn) * time.Second)

	log.Printf("token-injector: refreshed token, expires in %ds", expiresIn)
	return c.token, nil
}

func truncate(b []byte, max int) string {
	if len(b) <= max {
		return string(b)
	}
	return string(b[:max]) + "...(truncated)"
}

// ----------------------------------------------------------------------------
// Reverse proxy
// ----------------------------------------------------------------------------

func newProxy(cfg *config, tokens *tokenCache) http.Handler {
	rp := httputil.NewSingleHostReverseProxy(cfg.TargetURL)

	// Default Director rewrites Host/Scheme/Path; we wrap it to
	// inject the bearer token from the cache.
	defaultDirector := rp.Director
	rp.Director = func(req *http.Request) {
		defaultDirector(req)

		// Use upstream Host so TLS SNI + virtual hosting work.
		req.Host = cfg.TargetURL.Host

		token, err := tokens.Get(req.Context())
		if err != nil {
			// Stash the error so ErrorHandler can surface it. We can't
			// abort from inside Director, so we use a context value.
			*req = *req.WithContext(context.WithValue(req.Context(), tokenErrKey{}, err))
			return
		}
		req.Header.Set("Authorization", "Bearer "+token)
	}

	rp.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		if tokenErr, ok := r.Context().Value(tokenErrKey{}).(error); ok && tokenErr != nil {
			log.Printf("token-injector: token fetch failed: %v", tokenErr)
			http.Error(w, "token-injector: token fetch failed: "+tokenErr.Error(), http.StatusBadGateway)
			return
		}
		log.Printf("token-injector: upstream error: %v", err)
		http.Error(w, "token-injector: upstream error: "+err.Error(), http.StatusBadGateway)
	}

	rp.Transport = &http.Transport{
		ResponseHeaderTimeout: cfg.Timeout,
	}

	return rp
}

type tokenErrKey struct{}

// ----------------------------------------------------------------------------
// main
// ----------------------------------------------------------------------------

func main() {
	cfg, err := loadConfig()
	if err != nil {
		log.Fatalf("token-injector: config error: %v", err)
	}

	log.Printf("token-injector: target=%s tokenURL=%s clientID=%s scope=%q audience=%q listen=:%d",
		cfg.TargetURL.Redacted(), cfg.TokenURL, cfg.ClientID, cfg.Scope, cfg.Audience, cfg.ListenPort)

	tokens := newTokenCache(cfg)
	proxy := newProxy(cfg, tokens)

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.Handle("/", proxy)

	srv := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.ListenPort),
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	log.Printf("token-injector: listening on %s", srv.Addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("token-injector: server error: %v", err)
	}
}
