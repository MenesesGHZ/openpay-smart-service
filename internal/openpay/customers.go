package openpay

import (
	"context"
	"fmt"
)

// ─── OpenPay Customer object ──────────────────────────────────────────────────
// Maps to: POST/GET/PUT /v1/{merchantId}/customers

type CustomerAddress struct {
	Line1      string `json:"line1,omitempty"`
	Line2      string `json:"line2,omitempty"`
	City       string `json:"city,omitempty"`
	State      string `json:"state,omitempty"`
	PostalCode string `json:"postal_code,omitempty"`
	CountryCode string `json:"country_code,omitempty"`
}

type Customer struct {
	ID           string          `json:"id"`
	CreationDate string          `json:"creation_date"`
	Name         string          `json:"name"`
	LastName     string          `json:"last_name,omitempty"`
	Email        string          `json:"email"`
	PhoneNumber  string          `json:"phone_number,omitempty"`
	ExternalID   string          `json:"external_id,omitempty"`
	Status       string          `json:"status"`
	Balance      float64         `json:"balance"`
	Address      *CustomerAddress `json:"address,omitempty"`
	ClaveClient  string          `json:"clabe_transfer_account,omitempty"`
}

type CreateCustomerRequest struct {
	Name        string          `json:"name"`
	LastName    string          `json:"last_name,omitempty"`
	Email       string          `json:"email"`
	PhoneNumber string          `json:"phone_number,omitempty"`
	ExternalID  string          `json:"external_id,omitempty"`
	Address     *CustomerAddress `json:"address,omitempty"`
	SendEmail   bool            `json:"sends_email_notifications"`
}

type UpdateCustomerRequest struct {
	Name        string          `json:"name,omitempty"`
	LastName    string          `json:"last_name,omitempty"`
	Email       string          `json:"email,omitempty"`
	PhoneNumber string          `json:"phone_number,omitempty"`
	Address     *CustomerAddress `json:"address,omitempty"`
}

// CreateCustomer calls POST /v1/{merchantId}/customers
func (c *Client) CreateCustomer(ctx context.Context, req CreateCustomerRequest) (*Customer, error) {
	var out Customer
	if err := c.post(ctx, "/customers", req, &out); err != nil {
		return nil, fmt.Errorf("create customer: %w", err)
	}
	return &out, nil
}

// GetCustomer calls GET /v1/{merchantId}/customers/{id}
func (c *Client) GetCustomer(ctx context.Context, customerID string) (*Customer, error) {
	var out Customer
	if err := c.get(ctx, "/customers/"+customerID, &out); err != nil {
		return nil, fmt.Errorf("get customer %s: %w", customerID, err)
	}
	return &out, nil
}

// UpdateCustomer calls PUT /v1/{merchantId}/customers/{id}
func (c *Client) UpdateCustomer(ctx context.Context, customerID string, req UpdateCustomerRequest) (*Customer, error) {
	var out Customer
	if err := c.put(ctx, "/customers/"+customerID, req, &out); err != nil {
		return nil, fmt.Errorf("update customer %s: %w", customerID, err)
	}
	return &out, nil
}
