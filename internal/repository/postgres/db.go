// Package postgres provides pgx/v5-based implementations of the repository
// interfaces defined in internal/repository/interfaces.go.
package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/menesesghz/openpay-smart-service/internal/config"
)

// Connect creates a pgxpool.Pool from the service DatabaseConfig, verifies
// connectivity with a Ping, and returns the pool.
// The caller is responsible for calling pool.Close() on shutdown.
func Connect(ctx context.Context, cfg config.DatabaseConfig) (*pgxpool.Pool, error) {
	poolCfg, err := pgxpool.ParseConfig(cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("parse database DSN: %w", err)
	}

	if cfg.MaxOpenConns > 0 {
		poolCfg.MaxConns = int32(cfg.MaxOpenConns)
	}
	if cfg.ConnMaxLife > 0 {
		poolCfg.MaxConnLifetime = cfg.ConnMaxLife
	}
	// pgxpool does not have a MaxIdleConns concept; MinConns is the closest.
	if cfg.MaxIdleConns > 0 {
		poolCfg.MinConns = int32(cfg.MaxIdleConns)
	}

	// Give the pool up to 10 s to establish an initial connection.
	connectCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	pool, err := pgxpool.NewWithConfig(connectCtx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("create pgxpool: %w", err)
	}

	if err := pool.Ping(connectCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}

	return pool, nil
}
