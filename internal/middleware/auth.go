// Package middleware provides gRPC server interceptors for auth, logging, and rate limiting.
package middleware

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/menesesghz/openpay-smart-service/internal/domain"
	"github.com/menesesghz/openpay-smart-service/internal/repository"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type ctxKeyTenant struct{}

// TenantContext carries the authenticated tenant into every handler.
type TenantContext struct {
	Tenant    *domain.Tenant
	APIKeyRaw string // prefix only, e.g. "opk_live_xxxx…" — never the full key
}

// TenantFromContext retrieves the authenticated TenantContext from a request context.
func TenantFromContext(ctx context.Context) (*TenantContext, bool) {
	tc, ok := ctx.Value(ctxKeyTenant{}).(*TenantContext)
	return tc, ok
}

// AuthInterceptor returns a unary gRPC interceptor that validates tenant API keys.
// Keys are expected in the "authorization" metadata header as "Bearer <key>".
func AuthInterceptor(tenantRepo repository.TenantRepository) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		// Health check endpoints skip auth.
		if strings.HasPrefix(info.FullMethod, "/grpc.health") {
			return handler(ctx, req)
		}

		// Admin-authenticated requests (validated by AdminAuthInterceptor) skip
		// the tenant key lookup — there is no tenant context for admin calls.
		if isAdminAuthenticated(ctx) {
			return handler(ctx, req)
		}

		key, err := extractBearerToken(ctx)
		if err != nil {
			return nil, status.Error(codes.Unauthenticated, "missing or malformed Authorization header")
		}

		tenant, err := resolveTenant(ctx, tenantRepo, key)
		if err != nil {
			return nil, status.Error(codes.Unauthenticated, "invalid API key")
		}

		tc := &TenantContext{Tenant: tenant, APIKeyRaw: maskKey(key)}
		ctx = context.WithValue(ctx, ctxKeyTenant{}, tc)

		return handler(ctx, req)
	}
}

// StreamAuthInterceptor is the streaming variant of AuthInterceptor.
func StreamAuthInterceptor(tenantRepo repository.TenantRepository) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		ctx := ss.Context()

		key, err := extractBearerToken(ctx)
		if err != nil {
			return status.Error(codes.Unauthenticated, "missing or malformed Authorization header")
		}

		tenant, err := resolveTenant(ctx, tenantRepo, key)
		if err != nil {
			return status.Error(codes.Unauthenticated, "invalid API key")
		}

		tc := &TenantContext{Tenant: tenant, APIKeyRaw: maskKey(key)}
		wrapped := &wrappedStream{ServerStream: ss, ctx: context.WithValue(ctx, ctxKeyTenant{}, tc)}

		return handler(srv, wrapped)
	}
}

func extractBearerToken(ctx context.Context) (string, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return "", domain.ErrUnauthenticated
	}
	vals := md.Get("authorization")
	if len(vals) == 0 {
		return "", domain.ErrUnauthenticated
	}
	parts := strings.SplitN(vals[0], " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") {
		return "", domain.ErrUnauthenticated
	}
	return parts[1], nil
}

func resolveTenant(ctx context.Context, repo repository.TenantRepository, key string) (*domain.Tenant, error) {
	h := sha256.Sum256([]byte(key))
	hash := hex.EncodeToString(h[:])
	return repo.GetByAPIKeyHash(ctx, hash)
}

func maskKey(key string) string {
	if len(key) <= 12 {
		return "***"
	}
	return key[:12] + "…"
}

// wrappedStream injects a modified context into a server stream.
type wrappedStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (w *wrappedStream) Context() context.Context { return w.ctx }
