package api

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

// loggingInterceptor logs all RPC calls with structured logging
func loggingInterceptor(logger *slog.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		start := time.Now()
		logger.Info("gRPC request started",
			"method", info.FullMethod,
			"start_time", start.Format(time.RFC3339),
		)

		resp, err := handler(ctx, req)

		duration := time.Since(start)
		statusCode := codes.OK
		if err != nil {
			if s, ok := status.FromError(err); ok {
				statusCode = s.Code()
			}
		}

		logger.Info("gRPC request completed",
			"method", info.FullMethod,
			"duration_ms", duration.Milliseconds(),
			"status", statusCode.String(),
		)

		return resp, err
	}
}

// authInterceptor verifies mTLS client certificates
func authInterceptor(allowedOUs []string) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		p, ok := peer.FromContext(ctx)
		if !ok {
			return nil, status.Error(codes.Unauthenticated, "no peer info")
		}

		tlsInfo, ok := p.AuthInfo.(credentials.TLSInfo)
		if !ok {
			return nil, status.Error(codes.Unauthenticated, "no TLS info")
		}

		if len(tlsInfo.State.VerifiedChains) == 0 {
			return nil, status.Error(codes.Unauthenticated, "no verified certificate chain")
		}

		cert := tlsInfo.State.VerifiedChains[0][0]
		if len(allowedOUs) > 0 {
			ouMatch := false
			for _, ou := range allowedOUs {
				for _, certOU := range cert.Subject.OrganizationalUnit {
					if certOU == ou {
						ouMatch = true
						break
					}
				}
				if ouMatch {
					break
				}
			}
			if !ouMatch {
				return nil, status.Errorf(codes.PermissionDenied, "OU not in allowed list")
			}
		}

		return handler(ctx, req)
	}
}

// recoveryInterceptor recovers from panics and returns internal error
func recoveryInterceptor(logger *slog.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp interface{}, err error) {
		defer func() {
			if r := recover(); r != nil {
				logger.Error("gRPC panic recovered",
					"method", info.FullMethod,
					"panic", fmt.Sprintf("%+v", r),
				)
				err = status.Errorf(codes.Internal, "internal server error")
			}
		}()
		return handler(ctx, req)
	}
}

// rateLimiter implements a simple token bucket rate limiter
type rateLimiter struct {
	mu         sync.Mutex
	tokens     float64
	maxTokens  float64
	refillRate float64
	lastRefill time.Time
}

func newRateLimiter(maxPerSecond int) *rateLimiter {
	return &rateLimiter{
		tokens:     float64(maxPerSecond),
		maxTokens:  float64(maxPerSecond),
		refillRate: float64(maxPerSecond),
		lastRefill: time.Now(),
	}
}

func (rl *rateLimiter) allow() bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	elapsed := now.Sub(rl.lastRefill).Seconds()
	rl.tokens = min(rl.maxTokens, rl.tokens+elapsed*rl.refillRate)
	rl.lastRefill = now

	if rl.tokens >= 1 {
		rl.tokens--
		return true
	}
	return false
}

func min(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

// rateLimitInterceptor limits requests per second
func rateLimitInterceptor(maxPerSecond int) grpc.UnaryServerInterceptor {
	limiter := newRateLimiter(maxPerSecond)
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		if !limiter.allow() {
			return nil, status.Errorf(codes.ResourceExhausted, "rate limit exceeded: max %d req/s", maxPerSecond)
		}
		return handler(ctx, req)
	}
}
