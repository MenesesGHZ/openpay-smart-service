// Package openpay provides a typed HTTP client for the OpenPay by BBVA REST API.
// Docs: https://documents.openpay.mx/en/api
// Auth: HTTP Basic — private API key as username, empty password, over TLS 1.3.
//
// # Singleton pattern
//
// This client is constructed ONCE at service startup from the service-owner's
// OpenPay credentials (config.OpenPay). It is shared across all request handlers.
// There is no per-tenant client — all API calls go through the same merchant account.
//
// Customer payments (charges) accumulate in the merchant balance. The scheduler
// calls CreatePayout to SPEI-transfer each tenant's portion to their CLABE.
package openpay

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/rs/zerolog"
	"github.com/your-org/openpay-smart-service/internal/config"
	"github.com/your-org/openpay-smart-service/internal/domain"
)

// Client is a thread-safe OpenPay REST API client for the service-owner merchant.
type Client struct {
	baseURL    string // e.g. https://sandbox-api.openpay.mx/v1/{merchantID}
	privateKey string // HTTP Basic auth username — the merchant's private API key
	http       *http.Client
	log        zerolog.Logger
}

// NewClientFromConfig constructs the singleton OpenPay client from service config.
// Call once at startup; inject the result wherever OpenPay calls are needed.
func NewClientFromConfig(cfg config.OpenPayConfig, log zerolog.Logger) *Client {
	timeout := time.Duration(cfg.HTTPTimeoutMS) * time.Millisecond
	return newClient(cfg.BaseURL(), cfg.MerchantID, cfg.PrivateKey, timeout, log)
}

// newClient is the internal constructor used by NewClientFromConfig and tests.
func newClient(baseURL, merchantID, privateKey string, timeout time.Duration, log zerolog.Logger) *Client {
	return &Client{
		baseURL:    fmt.Sprintf("%s/%s", baseURL, merchantID),
		privateKey: privateKey,
		http: &http.Client{
			Timeout: timeout,
			Transport: &http.Transport{
				MaxIdleConns:       100,
				IdleConnTimeout:    90 * time.Second,
				DisableCompression: false,
			},
		},
		log: log.With().Str("component", "openpay_client").Logger(),
	}
}

// ─── Generic request helpers ─────────────────────────────────────────────────

func (c *Client) get(ctx context.Context, path string, out any) error {
	return c.do(ctx, http.MethodGet, path, nil, out)
}

func (c *Client) post(ctx context.Context, path string, body, out any) error {
	return c.do(ctx, http.MethodPost, path, body, out)
}

func (c *Client) put(ctx context.Context, path string, body, out any) error {
	return c.do(ctx, http.MethodPut, path, body, out)
}

func (c *Client) delete(ctx context.Context, path string) error {
	return c.do(ctx, http.MethodDelete, path, nil, nil)
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	url := c.baseURL + path

	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		bodyReader = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}

	req.SetBasicAuth(c.privateKey, "")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	// Per OpenPay anti-fraud requirement, propagate the client IP.
	if clientIP, ok := ctx.Value(ctxKeyClientIP{}).(string); ok && clientIP != "" {
		req.Header.Set("X-Forwarded-For", clientIP)
	}

	start := time.Now()
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %s %s: %v", domain.ErrUpstreamFailure, method, url, err)
	}
	defer resp.Body.Close()

	latency := time.Since(start)
	c.log.Debug().
		Str("method", method).
		Str("path", path).
		Int("status", resp.StatusCode).
		Dur("latency", latency).
		Msg("openpay request")

	if resp.StatusCode >= 400 {
		return c.parseError(resp)
	}

	if out != nil && resp.StatusCode != http.StatusNoContent {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}

	return nil
}

func (c *Client) parseError(resp *http.Response) error {
	var oe domain.OpenPayError
	if err := json.NewDecoder(resp.Body).Decode(&oe); err != nil {
		return fmt.Errorf("%w: HTTP %d", domain.ErrUpstreamFailure, resp.StatusCode)
	}
	oe.HTTPStatus = resp.StatusCode
	return domain.FromOpenPayError(&oe)
}

// ctxKeyClientIP is the context key used to propagate the caller's IP for X-Forwarded-For.
type ctxKeyClientIP struct{}

// WithClientIP attaches the client IP to a context for anti-fraud header forwarding.
func WithClientIP(ctx context.Context, ip string) context.Context {
	return context.WithValue(ctx, ctxKeyClientIP{}, ip)
}
