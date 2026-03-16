package middleware

import (
	"context"
	"time"

	"github.com/rs/zerolog"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// LoggingInterceptor returns a unary interceptor that logs every RPC call
// with tenant_id, method, duration, and gRPC status code.
func LoggingInterceptor(log zerolog.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		start := time.Now()

		resp, err := handler(ctx, req)

		code := codes.OK
		if err != nil {
			code = status.Code(err)
		}

		event := log.Info()
		if code != codes.OK {
			event = log.Error().Err(err)
		}

		tenantID := ""
		if tc, ok := TenantFromContext(ctx); ok {
			tenantID = tc.Tenant.ID.String()
		}

		event.
			Str("method", info.FullMethod).
			Str("tenant_id", tenantID).
			Str("grpc_code", code.String()).
			Int64("duration_ms", time.Since(start).Milliseconds()).
			Msg("rpc")

		return resp, err
	}
}
