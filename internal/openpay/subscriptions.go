package openpay

import (
	"context"
	"fmt"
)

// ─── OpenPay Plan object ──────────────────────────────────────────────────────
// Maps to: POST/GET /v1/{merchantId}/plans

type Plan struct {
	ID               string  `json:"id"`
	Name             string  `json:"name"`
	Status           string  `json:"status"` // "active" | "deleted"
	Amount           float64 `json:"amount"`
	Currency         string  `json:"currency"`
	CreationDate     string  `json:"creation_date"`
	RepeatEvery      int     `json:"repeat_every"` // e.g. 1
	RepeatUnit       string  `json:"repeat_unit"`  // "week" | "month" | "year"
	RetryTimes       int     `json:"retry_times"`
	StatusOnRetryEnd string  `json:"status_after_retry"` // "cancelled" | "unpaid"
	TrialDays        int     `json:"trial_days"`
}

type CreatePlanRequest struct {
	Name             string  `json:"name"`
	Amount           float64 `json:"amount"`
	Currency         string  `json:"currency"`
	RepeatEvery      int     `json:"repeat_every"`          // e.g. 1
	RepeatUnit       string  `json:"repeat_unit"`           // "week" | "month" | "year"
	RetryTimes       int     `json:"retry_times,omitempty"` // default 3
	StatusOnRetryEnd string  `json:"status_after_retry"`    // "cancelled" | "unpaid"
	TrialDays        int     `json:"trial_days,omitempty"`
}

// ─── OpenPay Subscription object ─────────────────────────────────────────────
// Maps to: POST/GET /v1/{merchantId}/customers/{customerId}/subscriptions

type Subscription struct {
	ID              string  `json:"id"`
	Status          string  `json:"status"`           // "trial" | "active" | "past_due" | "unpaid" | "cancelled"
	CustomerID      string  `json:"customer_id"`
	CreationDate    string  `json:"creation_date"`
	CancelledDate   string  `json:"cancelled_date,omitempty"`
	PeriodEndDate   string  `json:"period_end_date"` // current period end (ISO-8601 date)
	TrialEndDate    string  `json:"trial_end_date,omitempty"`
	PlanID          string  `json:"plan_id"`
	SourceID        string  `json:"card_id"` // card used for billing
	CurrentPeriodNumber int `json:"current_period_number"` // billing cycle count (1-based)
}

type CreateSubscriptionRequest struct {
	PlanID  string `json:"plan_id"`
	CardID  string `json:"card_id"` // stored card to charge each period
	// TrialEndDate overrides the plan's trial_days if set (ISO-8601 date "YYYY-MM-DD").
	TrialEndDate string `json:"trial_end_date,omitempty"`
}

// ─── Plan API ─────────────────────────────────────────────────────────────────

// CreatePlan calls POST /v1/{merchantId}/plans
func (c *Client) CreatePlan(ctx context.Context, req CreatePlanRequest) (*Plan, error) {
	var out Plan
	if err := c.post(ctx, "/plans", req, &out); err != nil {
		return nil, fmt.Errorf("create plan: %w", err)
	}
	return &out, nil
}

// GetPlan calls GET /v1/{merchantId}/plans/{planId}
func (c *Client) GetPlan(ctx context.Context, planID string) (*Plan, error) {
	var out Plan
	if err := c.get(ctx, "/plans/"+planID, &out); err != nil {
		return nil, fmt.Errorf("get plan %s: %w", planID, err)
	}
	return &out, nil
}

// ListPlans calls GET /v1/{merchantId}/plans
func (c *Client) ListPlans(ctx context.Context, params map[string]string) ([]Plan, error) {
	path := "/plans"
	if len(params) > 0 {
		path += "?" + encodeParams(params)
	}
	var out []Plan
	if err := c.get(ctx, path, &out); err != nil {
		return nil, fmt.Errorf("list plans: %w", err)
	}
	return out, nil
}

// DeletePlan calls DELETE /v1/{merchantId}/plans/{planId}
// After deletion, existing subscriptions complete their current period but are not renewed.
func (c *Client) DeletePlan(ctx context.Context, planID string) error {
	if err := c.delete(ctx, "/plans/"+planID); err != nil {
		return fmt.Errorf("delete plan %s: %w", planID, err)
	}
	return nil
}

// ─── Subscription API ─────────────────────────────────────────────────────────

// CreateSubscription calls POST /v1/{merchantId}/customers/{customerId}/subscriptions
func (c *Client) CreateSubscription(ctx context.Context, customerID string, req CreateSubscriptionRequest) (*Subscription, error) {
	var out Subscription
	path := fmt.Sprintf("/customers/%s/subscriptions", customerID)
	if err := c.post(ctx, path, req, &out); err != nil {
		return nil, fmt.Errorf("create subscription for customer %s: %w", customerID, err)
	}
	return &out, nil
}

// GetSubscription calls GET /v1/{merchantId}/customers/{customerId}/subscriptions/{subId}
func (c *Client) GetSubscription(ctx context.Context, customerID, subID string) (*Subscription, error) {
	var out Subscription
	path := fmt.Sprintf("/customers/%s/subscriptions/%s", customerID, subID)
	if err := c.get(ctx, path, &out); err != nil {
		return nil, fmt.Errorf("get subscription %s for customer %s: %w", subID, customerID, err)
	}
	return &out, nil
}

// ListSubscriptions calls GET /v1/{merchantId}/customers/{customerId}/subscriptions
func (c *Client) ListSubscriptions(ctx context.Context, customerID string, params map[string]string) ([]Subscription, error) {
	path := fmt.Sprintf("/customers/%s/subscriptions", customerID)
	if len(params) > 0 {
		path += "?" + encodeParams(params)
	}
	var out []Subscription
	if err := c.get(ctx, path, &out); err != nil {
		return nil, fmt.Errorf("list subscriptions for customer %s: %w", customerID, err)
	}
	return out, nil
}

// CancelSubscription calls DELETE /v1/{merchantId}/customers/{customerId}/subscriptions/{subId}
// OpenPay cancels the subscription at the end of the current billing period.
func (c *Client) CancelSubscription(ctx context.Context, customerID, subID string) error {
	path := fmt.Sprintf("/customers/%s/subscriptions/%s", customerID, subID)
	if err := c.delete(ctx, path); err != nil {
		return fmt.Errorf("cancel subscription %s for customer %s: %w", subID, customerID, err)
	}
	return nil
}
