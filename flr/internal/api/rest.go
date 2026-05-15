package api

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	flrv1 "github.com/otap/flr/proto/flr/v1"
)

// setupGateway creates and configures the gRPC-gateway
func (s *Server) setupGateway(ctx context.Context) (*runtime.ServeMux, error) {
	mux := runtime.NewServeMux(
		runtime.WithMarshalerOption(runtime.MIMEWildcard, &runtime.JSONPb{}),
	)

	if err := flrv1.RegisterFederatedRegistryHandlerServer(ctx, mux, s); err != nil {
		return nil, fmt.Errorf("failed to register gateway handlers: %w", err)
	}

	s.logger.Info("REST gateway configured")
	return mux, nil
}

// LoggingResponseWriter wraps http.ResponseWriter to capture status code
type LoggingResponseWriter struct {
	http.ResponseWriter
	statusCode int
}

func NewLoggingResponseWriter(w http.ResponseWriter) *LoggingResponseWriter {
	return &LoggingResponseWriter{w, http.StatusOK}
}

func (lrw *LoggingResponseWriter) WriteHeader(code int) {
	lrw.statusCode = code
	lrw.ResponseWriter.WriteHeader(code)
}

// HTTPLoggingMiddleware logs all HTTP requests
func HTTPLoggingMiddleware(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		lrw := NewLoggingResponseWriter(w)
		next.ServeHTTP(lrw, r)
		duration := time.Since(start)
		logger.Info("HTTP request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", lrw.statusCode,
			"duration_ms", duration.Milliseconds(),
			"remote_addr", r.RemoteAddr,
		)
	})
}
