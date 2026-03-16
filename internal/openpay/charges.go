package openpay

import (
	"context"
	"fmt"
)

// ─── OpenPay Charge object ────────────────────────────────────────────────────
// Maps to: POST/GET /v1/{merchantId}/customers/{customerId}/charges
//          GET      /v1/{merchantId}/charges

type ChargeCard struct {
	Type           string `json:"type"`
	Brand          string `json:"brand"`
	Address        *CustomerAddress `json:"address,omitempty"`
	CardNumber     string `json:"card_number"`     // masked, last four digits
	HolderName     string `json:"holder_name"`
	ExpirationYear string `json:"expiration_year"`
	ExpirationMonth string `json:"expiration_month"`
	AllowsCharges  bool   `json:"allows_charges"`
	AllowsPayouts  bool   `json:"allows_payouts"`
	BankName       string `json:"bank_name"`
	BankCode       string `json:"bank_code"`
}

// PaymentMethodDetails is returned for bank / store charges.
type PaymentMethodDetails struct {
	Type      string `json:"type"`       // bank_account | store
	Agreement string `json:"agreement,omitempty"` // Bancomer CIE
	Bank      string `json:"bank,omitempty"`
	CLABE     string `json:"clabe,omitempty"`
	Name      string `json:"name,omitempty"`
	Reference string `json:"reference,omitempty"`
	BarcodeURL string `json:"barcode_url,omitempty"` // for store charges
}

type Charge struct {
	ID              string                `json:"id"`
	CreationDate    string                `json:"creation_date"`
	OperationDate   string                `json:"operation_date"`
	TransactionType string                `json:"transaction_type"` // charge
	Status          string                `json:"status"`           // in_progress | completed | failed | cancelled | refunded | chargeback_pending | chargeback_in_review | chargeback_adjustment | charge_pending
	Amount          float64               `json:"amount"`
	Currency        string                `json:"currency"`
	Description     string                `json:"description"`
	OrderID         string                `json:"order_id,omitempty"`
	ErrorMessage    string                `json:"error_message,omitempty"`
	CustomerID      string                `json:"customer_id,omitempty"`
	Authorization   string                `json:"authorization,omitempty"`
	Method          string                `json:"method"` // card | bank_account | store
	Card            *ChargeCard           `json:"card,omitempty"`
	PaymentMethod   *PaymentMethodDetails `json:"payment_method,omitempty"`
	FeeDetails      *FeeDetails           `json:"fee_details,omitempty"`
}

type FeeDetails struct {
	Amount      float64 `json:"amount"`
	Tax         float64 `json:"tax"`
	Currency    string  `json:"currency"`
}

// CreateChargeRequest — card charge using a stored card ID.
type CreateCardChargeRequest struct {
	Method      string  `json:"method"`   // "card"
	SourceID    string  `json:"source_id"` // OpenPay card ID
	Amount      float64 `json:"amount"`
	Currency    string  `json:"currency"`
	Description string  `json:"description"`
	OrderID     string  `json:"order_id,omitempty"`
	DeviceSessionID string `json:"device_session_id,omitempty"` // anti-fraud
	Capture     bool    `json:"capture"` // true = immediate capture
}

// CreateBankChargeRequest — generates a SPEI CLABE for the customer to transfer to.
type CreateBankChargeRequest struct {
	Method      string  `json:"method"` // "bank_account"
	Amount      float64 `json:"amount"`
	Currency    string  `json:"currency"`
	Description string  `json:"description"`
	OrderID     string  `json:"order_id,omitempty"`
	DueDate     string  `json:"due_date,omitempty"` // ISO-8601
}

type RefundRequest struct {
	Description string   `json:"description"`
	Amount      *float64 `json:"amount,omitempty"` // nil = full refund
}

// CreateCharge calls POST /v1/{merchantId}/customers/{customerId}/charges
func (c *Client) CreateCharge(ctx context.Context, customerID string, req any) (*Charge, error) {
	var out Charge
	path := fmt.Sprintf("/customers/%s/charges", customerID)
	if err := c.post(ctx, path, req, &out); err != nil {
		return nil, fmt.Errorf("create charge for customer %s: %w", customerID, err)
	}
	return &out, nil
}

// GetCharge calls GET /v1/{merchantId}/charges/{transactionId}
func (c *Client) GetCharge(ctx context.Context, transactionID string) (*Charge, error) {
	var out Charge
	if err := c.get(ctx, "/charges/"+transactionID, &out); err != nil {
		return nil, fmt.Errorf("get charge %s: %w", transactionID, err)
	}
	return &out, nil
}

// GetCustomerCharge calls GET /v1/{merchantId}/customers/{customerId}/charges/{transactionId}
func (c *Client) GetCustomerCharge(ctx context.Context, customerID, transactionID string) (*Charge, error) {
	var out Charge
	path := fmt.Sprintf("/customers/%s/charges/%s", customerID, transactionID)
	if err := c.get(ctx, path, &out); err != nil {
		return nil, fmt.Errorf("get charge %s for customer %s: %w", transactionID, customerID, err)
	}
	return &out, nil
}

// ListCharges calls GET /v1/{merchantId}/charges with optional query params.
func (c *Client) ListCharges(ctx context.Context, params map[string]string) ([]Charge, error) {
	path := "/charges"
	if len(params) > 0 {
		path += "?" + encodeParams(params)
	}
	var out []Charge
	if err := c.get(ctx, path, &out); err != nil {
		return nil, fmt.Errorf("list charges: %w", err)
	}
	return out, nil
}

// ListCustomerCharges calls GET /v1/{merchantId}/customers/{customerId}/charges
func (c *Client) ListCustomerCharges(ctx context.Context, customerID string, params map[string]string) ([]Charge, error) {
	path := fmt.Sprintf("/customers/%s/charges", customerID)
	if len(params) > 0 {
		path += "?" + encodeParams(params)
	}
	var out []Charge
	if err := c.get(ctx, path, &out); err != nil {
		return nil, fmt.Errorf("list charges for customer %s: %w", customerID, err)
	}
	return out, nil
}

// RefundCharge calls POST /v1/{merchantId}/charges/{transactionId}/refund
func (c *Client) RefundCharge(ctx context.Context, transactionID string, req RefundRequest) (*Charge, error) {
	var out Charge
	path := fmt.Sprintf("/charges/%s/refund", transactionID)
	if err := c.post(ctx, path, req, &out); err != nil {
		return nil, fmt.Errorf("refund charge %s: %w", transactionID, err)
	}
	return &out, nil
}

// encodeParams builds a URL query string from a map.
func encodeParams(params map[string]string) string {
	var parts []string
	for k, v := range params {
		if v != "" {
			parts = append(parts, k+"="+v)
		}
	}
	result := ""
	for i, p := range parts {
		if i > 0 {
			result += "&"
		}
		result += p
	}
	return result
}
