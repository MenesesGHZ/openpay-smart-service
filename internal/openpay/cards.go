package openpay

import (
	"context"
	"fmt"
)

// ─── OpenPay Card object ──────────────────────────────────────────────────────
// Maps to: POST/GET/DELETE /v1/{merchantId}/customers/{customerId}/cards
// IMPORTANT: Never send raw card numbers through the service.
// Card tokens must be generated client-side via the OpenPay JS / mobile SDK.

type Card struct {
	ID              string `json:"id"`
	CreationDate    string `json:"creation_date"`
	Type            string `json:"type"`   // debit | credit | prepaid
	Brand           string `json:"brand"`  // visa | mastercard | carnet | amex
	CardNumber      string `json:"card_number"` // masked — last 4 digits only
	HolderName      string `json:"holder_name"`
	ExpirationYear  string `json:"expiration_year"`
	ExpirationMonth string `json:"expiration_month"`
	BankName        string `json:"bank_name"`
	BankCode        string `json:"bank_code"`
	AllowsCharges   bool   `json:"allows_charges"`
	AllowsPayouts   bool   `json:"allows_payouts"`
	CustomerID      string `json:"customer_id,omitempty"`
}

// CreateCardRequest accepts a one-time token from the OpenPay JS/mobile SDK.
// The token is valid for a single card creation call and contains the encrypted
// card data; the service never sees raw PANs.
type CreateCardRequest struct {
	TokenID         string `json:"token_id"`          // one-time token from SDK
	DeviceSessionID string `json:"device_session_id"` // anti-fraud session ID
}

// CreateCard calls POST /v1/{merchantId}/customers/{customerId}/cards
func (c *Client) CreateCard(ctx context.Context, customerID string, req CreateCardRequest) (*Card, error) {
	var out Card
	path := fmt.Sprintf("/customers/%s/cards", customerID)
	if err := c.post(ctx, path, req, &out); err != nil {
		return nil, fmt.Errorf("create card for customer %s: %w", customerID, err)
	}
	return &out, nil
}

// GetCard calls GET /v1/{merchantId}/customers/{customerId}/cards/{cardId}
func (c *Client) GetCard(ctx context.Context, customerID, cardID string) (*Card, error) {
	var out Card
	path := fmt.Sprintf("/customers/%s/cards/%s", customerID, cardID)
	if err := c.get(ctx, path, &out); err != nil {
		return nil, fmt.Errorf("get card %s for customer %s: %w", cardID, customerID, err)
	}
	return &out, nil
}

// ListCards calls GET /v1/{merchantId}/customers/{customerId}/cards
func (c *Client) ListCards(ctx context.Context, customerID string) ([]Card, error) {
	var out []Card
	path := fmt.Sprintf("/customers/%s/cards", customerID)
	if err := c.get(ctx, path, &out); err != nil {
		return nil, fmt.Errorf("list cards for customer %s: %w", customerID, err)
	}
	return out, nil
}

// DeleteCard calls DELETE /v1/{merchantId}/customers/{customerId}/cards/{cardId}
func (c *Client) DeleteCard(ctx context.Context, customerID, cardID string) error {
	path := fmt.Sprintf("/customers/%s/cards/%s", customerID, cardID)
	if err := c.delete(ctx, path); err != nil {
		return fmt.Errorf("delete card %s for customer %s: %w", cardID, customerID, err)
	}
	return nil
}
