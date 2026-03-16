package middleware

import (
	"context"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type ctxKeyAdmin struct{}

// isAdminAuthenticated returns true when the request has already been
// validated by AdminAuthInterceptor.  Used by AuthInterceptor to skip
// the tenant key lookup for admin paths.
func isAdminAuthenticated(ctx context.Context) bool {
	v, _ := ctx.Value(ctxKeyAdmin{}).(bool)
	return v
}

// isAdminMethod returns true for any RPC belonging to AdminTenantService.
func isAdminMethod(fullMethod string) bool {
	return strings.HasPrefix(fullMethod, "/openpay.v1.AdminTenantService/")
}

// AdminAuthInterceptor validates the static admin API key for all
// AdminTenantService RPCs.  Non-admin RPCs are passed through unchanged so
// the regular AuthInterceptor can handle them.
//
// Place this interceptor BEFORE AuthInterceptor in the chain:
//
//	grpc.ChainUnaryInterceptor(
//	    middleware.LoggingInterceptor(...),
//	    middleware.AdminAuthInterceptor(cfg.Admin.APIKey),  // ← first
//	    middleware.AuthInterceptor(tenantRepo),
//	    middleware.RateLimitInterceptor(...),
//	)
func AdminAuthInterceptor(adminKey string) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if !isAdminMethod(info.FullMethod) {
			// Not an admin endpoint — let the next interceptor handle it.
			return handler(ctx, req)
		}

		if adminKey == "" {
			return nil, status.Error(codes.Unauthenticated, "admin API is not configured on this server")
		}

		key, err := extractBearerToken(ctx)
		if err != nil || key != adminKey {
			return nil, status.Error(codes.Unauthenticated, "invalid admin API key")
		}

		// Mark context so downstream interceptors (AuthInterceptor) skip
		// the tenant lookup for this request.
		ctx = context.WithValue(ctx, ctxKeyAdmin{}, true)
		return handler(ctx, req)
	}
}
