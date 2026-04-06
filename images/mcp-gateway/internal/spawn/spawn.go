// Package spawn implements the MCP gateway's spawn mode.
//
// Spawn mode launches an MCP server subprocess communicating over stdio
// (JSON-RPC over stdin/stdout), and exposes it as an HTTP+SSE endpoint
// using MCP's Streamable HTTP transport.
//
// The subprocess command comes from the container's CMD/args (passed through
// from the MCPServer CR's spec.command). The gateway wraps it with an HTTP
// server that bridges JSON-RPC messages over stdio to HTTP POST requests and
// SSE event streams.
package spawn

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
)

// jsonRPCMessage represents a JSON-RPC 2.0 message.
type jsonRPCMessage struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id,omitempty"`
	Method  string           `json:"method,omitempty"`
	Params  json.RawMessage  `json:"params,omitempty"`
	Result  json.RawMessage  `json:"result,omitempty"`
	Error   *jsonRPCError    `json:"error,omitempty"`
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Server manages a subprocess and bridges it to HTTP.
type Server struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser

	mu       sync.Mutex
	pending  map[string]chan *jsonRPCMessage // id -> response channel
	sseConns map[string]chan *jsonRPCMessage // session -> notification channel

	logger *slog.Logger
}

// New creates a new spawn server for the given command.
func New(command []string, env []string, logger *slog.Logger) *Server {
	return &Server{
		cmd: func() *exec.Cmd {
			c := exec.Command(command[0], command[1:]...) //nolint:gosec // command from trusted operator config
			c.Env = append(os.Environ(), env...)
			c.Stderr = os.Stderr
			return c
		}(),
		pending:  make(map[string]chan *jsonRPCMessage),
		sseConns: make(map[string]chan *jsonRPCMessage),
		logger:   logger,
	}
}

// Start launches the subprocess and begins reading its stdout.
func (s *Server) Start(ctx context.Context) error {
	var err error
	s.stdin, err = s.cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdin pipe: %w", err)
	}
	s.stdout, err = s.cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}

	if err := s.cmd.Start(); err != nil {
		return fmt.Errorf("start subprocess: %w", err)
	}

	go s.readLoop(ctx)
	return nil
}

// readLoop reads JSON-RPC messages from the subprocess stdout.
func (s *Server) readLoop(ctx context.Context) {
	scanner := bufio.NewScanner(s.stdout)
	// MCP messages can be large (tool results with file contents, etc.)
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return
		default:
		}

		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var msg jsonRPCMessage
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			s.logger.Warn("failed to parse subprocess output", "error", err, "line", truncate(line, 200))
			continue
		}

		if msg.ID != nil {
			// Response to a request
			idStr := string(*msg.ID)
			s.mu.Lock()
			ch, ok := s.pending[idStr]
			s.mu.Unlock()
			if ok {
				ch <- &msg
			} else {
				s.logger.Warn("response for unknown request", "id", idStr)
			}
		} else {
			// Notification — broadcast to SSE connections
			s.mu.Lock()
			for _, ch := range s.sseConns {
				select {
				case ch <- &msg:
				default:
					s.logger.Warn("SSE channel full, dropping notification")
				}
			}
			s.mu.Unlock()
		}
	}

	if err := scanner.Err(); err != nil {
		s.logger.Error("subprocess stdout read error", "error", err)
	}
}

// Send writes a JSON-RPC request to the subprocess and waits for the response.
func (s *Server) Send(ctx context.Context, msg *jsonRPCMessage) (*jsonRPCMessage, error) {
	if msg.ID == nil {
		// Notification — just send, no response expected
		return nil, s.write(msg)
	}

	idStr := string(*msg.ID)
	ch := make(chan *jsonRPCMessage, 1)

	s.mu.Lock()
	s.pending[idStr] = ch
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		delete(s.pending, idStr)
		s.mu.Unlock()
	}()

	if err := s.write(msg); err != nil {
		return nil, err
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case resp := <-ch:
		return resp, nil
	}
}

func (s *Server) write(msg *jsonRPCMessage) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal message: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.stdin.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("write to subprocess: %w", err)
	}
	return nil
}

// RegisterSSE registers an SSE connection for receiving notifications.
func (s *Server) RegisterSSE(id string) chan *jsonRPCMessage {
	ch := make(chan *jsonRPCMessage, 64)
	s.mu.Lock()
	s.sseConns[id] = ch
	s.mu.Unlock()
	return ch
}

// UnregisterSSE removes an SSE connection.
func (s *Server) UnregisterSSE(id string) {
	s.mu.Lock()
	delete(s.sseConns, id)
	s.mu.Unlock()
}

// Wait waits for the subprocess to exit.
func (s *Server) Wait() error {
	return s.cmd.Wait()
}

// Handler returns an HTTP handler implementing MCP Streamable HTTP transport.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// POST /mcp — JSON-RPC request/response
	mux.HandleFunc("POST /mcp", s.handlePost)

	// GET /mcp — SSE stream for server-initiated notifications
	mux.HandleFunc("GET /mcp", s.handleSSE)

	// Health check
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, `{"status":"ok"}`)
	})

	return mux
}

func (s *Server) handlePost(w http.ResponseWriter, r *http.Request) {
	var msg jsonRPCMessage
	if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	resp, err := s.Send(r.Context(), &msg)
	if err != nil {
		s.logger.Error("send to subprocess failed", "error", err)
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}

	if resp == nil {
		// Notification — no response
		w.WriteHeader(http.StatusAccepted)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	sessionID := r.RemoteAddr
	ch := s.RegisterSSE(sessionID)
	defer s.UnregisterSSE(sessionID)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher.Flush()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-ch:
			data, err := json.Marshal(msg)
			if err != nil {
				s.logger.Error("marshal SSE message", "error", err)
				continue
			}
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
