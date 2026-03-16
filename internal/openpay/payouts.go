package openpay

import (
	"context"
	"fmt"
)

// ─── OpenPay Payout object ────────────────────────────────────────────────────
// Maps to: POST/GET /v1/{merchantId}/payouts

type BankAccount struct {
	CLABE       string `json:"clabe"`
	BankCode    string `json:"bank_code,omitempty"`
	BankName    string `json:"bank_name,omitempty"`
	AliasAs     string `json:"alias,omitempty"`
	HolderName  string `json:"holder_name"`
}

type Payout struct {
	ID              string       `json:"id"`
	CreationDate    string       `json:"creation_date"`
	OperationDate   string       `json:"operation_date,omitempty"`
	TransactionType string       `json:"transaction_type"` // payout
	Status          string       `json:"status"`           // pending | in_progress | completed | failed
	Amount          float64      `json:"amount"`
	Currency        string       `json:"currency"`
	Description     string       `json:"description"`
	OrderID         string       `json:"order_id,omitempty"`
	Method          string       `json:"method"` // bank_account
	BankAccount     *BankAccount `json:"bank_account,omitempty"`
	ErrorMessage    string       `json:"error_message,omitempty"`
}

type CreatePayoutRequest struct {
	Method      string      `json:"method"` // "bank_account"
	Amount      float64     `json:"amount"`
	Currency    string      `json:"currency"`
	Description string      `json:"description"`
	OrderID     string      `json:"order_id,omitempty"`
	BankAccount BankAccount `json:"bank_account"`
}

// CreatePayout calls POST /v1/{merchantId}/payouts
func (c *Client) CreatePayout(ctx context.Context, req CreatePayoutRequest) (*Payout, error) {
	var out Payout
	if err := c.post(ctx, "/payouts", req, &out); err != nil {
		return nil, fmt.Errorf("create payout: %w", err)
	}
	return &out, nil
}

// GetPayout calls GET /v1/{merchantId}/payouts/{transactionId}
func (c *Client) GetPayout(ctx context.Context, transactionID string) (*Payout, error) {
	var out Payout
	if err := c.get(ctx, "/payouts/"+transactionID, &out); err != nil {
		return nil, fmt.Errorf("get payout %s: %w", transactionID, err)
	}
	return &out, nil
}
