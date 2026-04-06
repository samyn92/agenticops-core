// mcp-gateway is the MCP protocol gateway for agenticops-core.
//
// It runs in two modes:
//
//   - spawn: Wraps an MCP server subprocess (stdio) and exposes it as
//     HTTP+SSE (MCP Streamable HTTP transport). Used in MCPServer
//     deploy-mode Deployments.
//
//   - proxy: Reverse-proxies HTTP+SSE to an upstream MCP server,
//     enforcing per-agent deny/allow permission rules on tools/call
//     requests. Used as a sidecar in Agent pods.
//
// Configuration is via environment variables:
//
//	GATEWAY_MODE        — "spawn" or "proxy" (required)
//	GATEWAY_PORT        — listen port (default: 8080)
//	GATEWAY_UPSTREAM    — upstream MCP server URL (proxy mode only)
//	GATEWAY_CONFIG      — path to permissions.json (proxy mode only)
//	GATEWAY_SERVER_NAME — MCP server name for policy lookup (proxy mode only)
//	GATEWAY_COMMAND     — command to spawn (spawn mode, if not using container CMD)
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/samyn92/agenticops-core/images/mcp-gateway/internal/permissions"
	"github.com/samyn92/agenticops-core/images/mcp-gateway/internal/proxy"
	"github.com/samyn92/agenticops-core/images/mcp-gateway/internal/spawn"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
		Level: logLevel(),
	}))

	mode := os.Getenv("GATEWAY_MODE")
	port := envOrDefault("GATEWAY_PORT", "8080")

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()

	var handler http.Handler
	var err error

	switch mode {
	case "spawn":
		handler, err = runSpawn(ctx, logger)
	case "proxy":
		handler, err = runProxy(logger)
	default:
		logger.Error("GATEWAY_MODE must be 'spawn' or 'proxy'", "mode", mode)
		os.Exit(1)
	}

	if err != nil {
		logger.Error("failed to initialize gateway", "mode", mode, "error", err)
		os.Exit(1)
	}

	addr := fmt.Sprintf(":%s", port)
	srv := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	logger.Info("mcp-gateway starting", "mode", mode, "addr", addr)

	go func() {
		<-ctx.Done()
		logger.Info("shutting down")
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		srv.Shutdown(shutdownCtx) //nolint:errcheck
	}()

	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		logger.Error("server error", "error", err)
		os.Exit(1)
	}
}

func runSpawn(ctx context.Context, logger *slog.Logger) (http.Handler, error) {
	// Get command from GATEWAY_COMMAND or remaining args
	command := getSpawnCommand()
	if len(command) == 0 {
		return nil, fmt.Errorf("no command specified: set GATEWAY_COMMAND or pass command as container args")
	}

	logger.Info("spawn mode", "command", command)

	// Collect env vars (everything except GATEWAY_* to avoid polluting the subprocess)
	var env []string
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "GATEWAY_") {
			env = append(env, e)
		}
	}

	server := spawn.New(command, env, logger)
	if err := server.Start(ctx); err != nil {
		return nil, fmt.Errorf("start subprocess: %w", err)
	}

	// Wait for subprocess in background
	go func() {
		if err := server.Wait(); err != nil {
			logger.Error("subprocess exited with error", "error", err)
			os.Exit(1)
		}
		logger.Info("subprocess exited cleanly")
		os.Exit(0)
	}()

	return server.Handler(), nil
}

func runProxy(logger *slog.Logger) (http.Handler, error) {
	upstream := os.Getenv("GATEWAY_UPSTREAM")
	if upstream == "" {
		return nil, fmt.Errorf("GATEWAY_UPSTREAM is required in proxy mode")
	}

	serverName := os.Getenv("GATEWAY_SERVER_NAME")
	configPath := os.Getenv("GATEWAY_CONFIG")

	var policy *permissions.ServerPolicy

	if configPath != "" {
		cfg, err := permissions.Load(configPath)
		if err != nil {
			logger.Warn("failed to load permissions config, running without policy", "error", err, "path", configPath)
		} else if serverName != "" {
			if p, ok := cfg[serverName]; ok {
				policy = &p
				logger.Info("loaded policy", "server", serverName, "mode", p.Mode, "rules", len(p.Rules))
			}
		}
	}

	logger.Info("proxy mode", "upstream", upstream, "server", serverName, "hasPolicy", policy != nil)

	server, err := proxy.New(upstream, serverName, policy, logger)
	if err != nil {
		return nil, err
	}

	return server.Handler(), nil
}

func getSpawnCommand() []string {
	// First try GATEWAY_COMMAND env
	if cmd := os.Getenv("GATEWAY_COMMAND"); cmd != "" {
		return strings.Fields(cmd)
	}

	// Otherwise use args after "--" (from container CMD)
	args := os.Args[1:]
	for i, arg := range args {
		if arg == "--" {
			return args[i+1:]
		}
	}

	// Or just all args
	return args
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func logLevel() slog.Level {
	switch strings.ToLower(os.Getenv("LOG_LEVEL")) {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
