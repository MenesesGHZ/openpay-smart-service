package middleware

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// TenantTier defines the rate limit bucket for a tenant.
type TenantTier string

const (
	TierFree       TenantTier = "free"
	TierStandard   TenantTier = "standard"
	TierEnterprise TenantTier = "enterprise"
)

// tierLimits maps a tier to (requests per minute, burst).
var tierLimits = map[TenantTier]struct{ rpm, burst int }{
	TierFree:       {60, 20},
	TierStandard:   {600, 100},
	TierEnterprise: {6000, 500},
}

// RateLimitInterceptor returns a unary interceptor that enforces per-tenant
// sliding window rate limits using a Redis counter.
func RateLimitInterceptor(rdb *redis.Client, getTier func(tenantID string) TenantTier) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		tc, ok := TenantFromContext(ctx)
		if !ok {
			return handler(ctx, req)
		}

		tenantID := tc.Tenant.ID.String()
		tier := getTier(tenantID)
		limits := tierLimits[tier]

		allowed, remaining, err := slidingWindowAllow(ctx, rdb, tenantID, limits.rpm)
		if err != nil {
			// On Redis failure, fail open (allow the request).
			return handler(ctx, req)
		}

		if !allowed {
			_ = remaining
			return nil, status.Errorf(codes.ResourceExhausted,
				"rate limit exceeded for tenant %s (tier=%s, limit=%d req/min)",
				tenantID, tier, limits.rpm)
		}

		return handler(ctx, req)
	}
}

// slidingWindowAllow implements a simple Redis-based sliding window counter.
// Returns (allowed, remaining, error).
func slidingWindowAllow(ctx context.Context, rdb *redis.Client, tenantID string, rpm int) (bool, int, error) {
	now := time.Now()
	windowKey := fmt.Sprintf("ratelimit:%s:%d", tenantID, now.Unix()/60) // 1-minute window

	pipe := rdb.Pipeline()
	incr := pipe.Incr(ctx, windowKey)
	pipe.Expire(ctx, windowKey, 90*time.Second) // slightly longer than window
	if _, err := pipe.Exec(ctx); err != nil {
		return true, rpm, err
	}

	count := int(incr.Val())
	if count > rpm {
		return false, 0, nil
	}
	return true, rpm - count, nil
}
