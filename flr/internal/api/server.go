// Package api hosts the FLR's HTTP REST surface. The original code drop
// included a gRPC + grpc-gateway transport that depended on protoc-generated
// code in `flr/proto/flr/v1`. That generated code is not in the repo and the
// Rust client (`otap-flr-client`) and the `flr-seed` CLI only consume the
// HTTP/REST schema endpoints, so the gRPC half is now omitted entirely.
package api

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/otap/flr/internal/config"
	"github.com/otap/flr/internal/crypto"
	"github.com/otap/flr/internal/federation"
	"github.com/otap/flr/internal/registry"
	"github.com/otap/flr/internal/xlat"
)

// Server is the FLR HTTP API server.
type Server struct {
	httpServer *http.Server
	registry   *registry.Engine
	federation *federation.Manager
	xlatMgr    *xlat.Manager
	crypto     *crypto.Engine
	cfg        *config.Config
	logger     *slog.Logger
}

// NewServer creates the HTTP API server.
func NewServer(cfg *config.Config, reg *registry.Engine, fed *federation.Manager, xlatMgr *xlat.Manager, crypt *crypto.Engine, logger *slog.Logger) (*Server, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is required")
	}
	if reg == nil {
		return nil, fmt.Errorf("registry engine is required")
	}
	if fed == nil {
		return nil, fmt.Errorf("federation manager is required")
	}
	if xlatMgr == nil {
		return nil, fmt.Errorf("translation manager is required")
	}
	if crypt == nil {
		return nil, fmt.Errorf("crypto engine is required")
	}
	if logger == nil {
		logger = slog.Default()
	}

	return &Server{
		registry:   reg,
		federation: fed,
		xlatMgr:    xlatMgr,
		crypto:     crypt,
		cfg:        cfg,
		logger:     logger.With("component", "api-server"),
	}, nil
}

// Start launches the HTTP server. It returns once the listener is ready
// (the server itself runs in a goroutine until Shutdown).
func (s *Server) Start(ctx context.Context) error {
	s.logger.Info("starting HTTP API server", "addr", s.cfg.Server.HTTPAddr)

	mux := http.NewServeMux()
	s.RegisterSchemaRoutes(mux)

	readTimeout := s.cfg.Server.ReadTimeout
	if readTimeout == 0 {
		readTimeout = 30 * time.Second
	}
	writeTimeout := s.cfg.Server.WriteTimeout
	if writeTimeout == 0 {
		writeTimeout = 30 * time.Second
	}

	s.httpServer = &http.Server{
		Addr:         s.cfg.Server.HTTPAddr,
		Handler:      HTTPLoggingMiddleware(s.logger, mux),
		ReadTimeout:  readTimeout,
		WriteTimeout: writeTimeout,
	}

	go func() {
		s.logger.Info("HTTP server listening", "addr", s.cfg.Server.HTTPAddr)
		if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			s.logger.Error("HTTP server error", "error", err)
		}
	}()

	return nil
}

// Shutdown gracefully stops the server.
func (s *Server) Shutdown(ctx context.Context) error {
	s.logger.Info("shutting down API server")
	if s.httpServer == nil {
		return nil
	}
	if err := s.httpServer.Shutdown(ctx); err != nil {
		return fmt.Errorf("HTTP shutdown: %w", err)
	}
	s.logger.Info("HTTP server stopped")
	return nil
}

// LoggingResponseWriter wraps http.ResponseWriter to capture the status code.
type LoggingResponseWriter struct {
	http.ResponseWriter
	statusCode int
}

// NewLoggingResponseWriter constructs a status-capturing response writer.
func NewLoggingResponseWriter(w http.ResponseWriter) *LoggingResponseWriter {
	return &LoggingResponseWriter{w, http.StatusOK}
}

// WriteHeader records the status code before forwarding it.
func (lrw *LoggingResponseWriter) WriteHeader(code int) {
	lrw.statusCode = code
	lrw.ResponseWriter.WriteHeader(code)
}

// HTTPLoggingMiddleware logs every HTTP request.
func HTTPLoggingMiddleware(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		lrw := NewLoggingResponseWriter(w)
		next.ServeHTTP(lrw, r)
		logger.Info("HTTP request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", lrw.statusCode,
			"duration_ms", time.Since(start).Milliseconds(),
			"remote_addr", r.RemoteAddr,
		)
	})
}
