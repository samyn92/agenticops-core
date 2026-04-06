// Package proxy implements the MCP gateway's proxy mode.
//
// Proxy mode is a reverse proxy that sits between an agent pod and an
// upstream MCP server (another pod's mcp-gateway in spawn mode, or an
// external MCP server). It intercepts JSON-RPC "tools/call" requests
// and enforces deny/allow permission rules before forwarding.
package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"github.com/samyn92/agenticops-core/images/mcp-gateway/internal/permissions"
)

// Server is the proxy-mode gateway.
type Server struct {
	upstream   *url.URL
	serverName string
	policy     *permissions.ServerPolicy
	proxy      *httputil.ReverseProxy
	logger     *slog.Logger
}

// New creates a proxy server.
func New(upstreamURL string, serverName string, policy *permissions.ServerPolicy, logger *slog.Logger) (*Server, error) {
	u, err := url.Parse(upstreamURL)
	if err != nil {
		return nil, fmt.Errorf("parse upstream URL: %w", err)
	}

	s := &Server{
		upstream:   u,
		serverName: serverName,
		policy:     policy,
		logger:     logger,
	}

	s.proxy = &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = u.Scheme
			req.URL.Host = u.Host
			req.Host = u.Host
		},
	}

	return s, nil
}

// jsonRPCRequest is the subset of JSON-RPC 2.0 we need to inspect.
type jsonRPCRequest struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id,omitempty"`
	Method  string           `json:"method"`
	Params  json.RawMessage  `json:"params,omitempty"`
}

// toolsCallParams is the params for a tools/call method.
type toolsCallParams struct {
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments"`
}

// Handler returns the HTTP handler for the proxy.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// POST /mcp — intercept tools/call, forward everything else
	mux.HandleFunc("POST /mcp", s.handlePost)

	// GET /mcp — SSE passthrough (no interception needed)
	mux.HandleFunc("GET /mcp", s.handlePassthrough)

	// Health check
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, `{"status":"ok"}`)
	})

	// Anything else — passthrough
	mux.HandleFunc("/", s.handlePassthrough)

	return mux
}

func (s *Server) handlePassthrough(w http.ResponseWriter, r *http.Request) {
	s.proxy.ServeHTTP(w, r)
}

func (s *Server) handlePost(w http.ResponseWriter, r *http.Request) {
	// Read body so we can inspect it
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, `{"error":"failed to read body"}`, http.StatusBadRequest)
		return
	}
	r.Body.Close()

	// Try to parse as JSON-RPC to check method
	var req jsonRPCRequest
	if err := json.Unmarshal(body, &req); err != nil {
		// Not valid JSON-RPC, forward as-is
		r.Body = io.NopCloser(bytes.NewReader(body))
		r.ContentLength = int64(len(body))
		s.proxy.ServeHTTP(w, r)
		return
	}

	// Only intercept tools/call
	if req.Method == "tools/call" {
		if !s.checkToolCall(w, &req) {
			return // blocked
		}
	}

	// Forward the request
	r.Body = io.NopCloser(bytes.NewReader(body))
	r.ContentLength = int64(len(body))
	s.proxy.ServeHTTP(w, r)
}

// checkToolCall evaluates permissions for a tools/call request.
// Returns true if allowed, false if blocked (error already written to w).
func (s *Server) checkToolCall(w http.ResponseWriter, req *jsonRPCRequest) bool {
	if s.policy == nil {
		return true // no policy = allow all
	}

	var params toolsCallParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		s.logger.Warn("failed to parse tools/call params", "error", err)
		return true // can't parse = allow (fail open)
	}

	if !s.policy.Check(params.Name, params.Arguments) {
		s.logger.Warn("tool call blocked by policy",
			"server", s.serverName,
			"tool", params.Name,
			"mode", s.policy.Mode,
		)

		// Return a JSON-RPC error response
		errResp := map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      req.ID,
			"error": map[string]interface{}{
				"code":    -32600,
				"message": fmt.Sprintf("tool call %q blocked by gateway policy (%s mode)", params.Name, s.policy.Mode),
			},
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(errResp)
		return false
	}

	s.logger.Debug("tool call allowed",
		"server", s.serverName,
		"tool", params.Name,
	)
	return true
}

// StripPrefix returns an HTTP handler that strips a URL path prefix.
// Used when the gateway is mounted at a subpath.
func StripPrefix(prefix string, handler http.Handler) http.Handler {
	if prefix == "" || prefix == "/" {
		return handler
	}
	return http.StripPrefix(strings.TrimRight(prefix, "/"), handler)
}
