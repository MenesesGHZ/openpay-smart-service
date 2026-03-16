package domain

import (
	"errors"
	"fmt"
)

// Sentinel errors — use errors.Is() for matching.
var (
	ErrNotFound          = errors.New("resource not found")
	ErrAlreadyExists     = errors.New("resource already exists")
	ErrInvalidArgument   = errors.New("invalid argument")
	ErrPermissionDenied  = errors.New("permission denied")
	ErrUnauthenticated   = errors.New("unauthenticated")
	ErrRateLimitExceeded = errors.New("rate limit exceeded")
	ErrUpstreamFailure   = errors.New("upstream service failure")
	ErrLinkExpired       = errors.New("payment link has expired")
	ErrLinkRedeemed      = errors.New("payment link already redeemed")
	ErrKYCRequired       = errors.New("member KYC verification required")
)

// OpenPayError wraps an error returned by the OpenPay REST API.
// Error codes reference: https://documents.openpay.mx/en/api#errors
type OpenPayError struct {
	HTTPStatus  int    `json:"http_status"`
	Category    string `json:"category"`     // e.g. "request", "transaction"
	ErrorCode   int    `json:"error_code"`   // e.g. 3001
	Description string `json:"description"`
	RequestID   string `json:"request_id"`
}

func (e *OpenPayError) Error() string {
	return fmt.Sprintf("openpay error %d (%s): %s [request_id=%s]",
		e.ErrorCode, e.Category, e.Description, e.RequestID)
}

// WellKnownOpenPayErrors maps numeric codes to sentinel domain errors.
// Full list: https://documents.openpay.mx/en/api#error-codes
var WellKnownOpenPayErrors = map[int]error{
	1000: ErrUpstreamFailure,   // Internal error
	1001: ErrInvalidArgument,   // Malformed JSON
	1002: ErrUnauthenticated,   // Authentication failed
	1003: ErrInvalidArgument,   // Parameter not found or wrong format
	1004: ErrUpstreamFailure,   // Service unavailable
	1005: ErrNotFound,          // Resource not found
	1006: ErrAlreadyExists,     // Duplicated transaction
	1007: ErrUpstreamFailure,   // Transfer between accounts failed
	1008: ErrUpstreamFailure,   // Account temporarily disabled
	1009: ErrInvalidArgument,   // Request body too large
	1010: ErrPermissionDenied,  // Public key used where private key required
	2001: ErrAlreadyExists,     // Bank account already registered
	2003: ErrAlreadyExists,     // Customer with that external_id already exists
	2004: ErrInvalidArgument,   // Invalid card check digit (Luhn)
	2005: ErrInvalidArgument,   // Card expiry date is in the past
	2006: ErrInvalidArgument,   // CVV required
	2007: ErrInvalidArgument,   // Card number is test-only
	2008: ErrNotFound,          // Card not valid for this account
	2009: ErrInvalidArgument,   // CVV not valid
	2010: ErrInvalidArgument,   // 3D-Secure authentication failed
	2011: ErrInvalidArgument,   // Unsupported card type
	3001: ErrUpstreamFailure,   // Card declined
	3002: ErrUpstreamFailure,   // Card expired
	3003: ErrUpstreamFailure,   // Insufficient funds
	3004: ErrUpstreamFailure,   // Card reported stolen
	3005: ErrUpstreamFailure,   // Fraud suspected
	3006: ErrUpstreamFailure,   // Unrecognised operation
	3008: ErrUpstreamFailure,   // Card not supported for this transaction type
	3009: ErrUpstreamFailure,   // Card reported lost
	3010: ErrUpstreamFailure,   // Bank restricted card
	3011: ErrUpstreamFailure,   // Bank authorisation requested
	3012: ErrPermissionDenied,  // PIN required
}

// FromOpenPayError converts an OpenPayError to a domain sentinel where known.
func FromOpenPayError(oe *OpenPayError) error {
	if err, ok := WellKnownOpenPayErrors[oe.ErrorCode]; ok {
		return fmt.Errorf("%w: %s", err, oe.Description)
	}
	return ErrUpstreamFailure
}
