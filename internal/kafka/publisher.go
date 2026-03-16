// Package kafka provides Kafka producer implementations for the service's
// event publishing needs.
package kafka

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/rs/zerolog"
	kafkago "github.com/segmentio/kafka-go"

	"github.com/menesesghz/openpay-smart-service/internal/domain"
)

// Publisher implements webhook.EventPublisher and writes payment events to the
// `payment.events` Kafka topic in JSON format.
//
// Each published message uses the payment_id as the Kafka key (same-partition
// delivery for the same payment) and the JSON-encoded PaymentEvent as the value.
type Publisher struct {
	writer *kafkago.Writer
	log    zerolog.Logger
}

// NewPublisher creates a Publisher connected to the given brokers.
// topic should be cfg.Kafka.TopicPaymentEvents (default: "payment.events").
func NewPublisher(brokers []string, topic string, log zerolog.Logger) *Publisher {
	w := &kafkago.Writer{
		Addr:         kafkago.TCP(brokers...),
		Topic:        topic,
		Balancer:     &kafkago.Hash{}, // same key → same partition
		BatchTimeout: 10 * time.Millisecond,
		// RequiredAcks: All ensures the message is durably stored on all ISR replicas.
		RequiredAcks: kafkago.RequireAll,
		// Async = false: PublishPaymentEvent blocks until the broker acks.
		// Switch to true for higher throughput at the cost of losing in-flight messages on crash.
		Async: false,
	}

	return &Publisher{
		writer: w,
		log:    log.With().Str("component", "kafka_publisher").Logger(),
	}
}

// PublishPaymentEvent serialises evt to JSON and writes it to the payment.events
// topic.  Implements webhook.EventPublisher.
func (p *Publisher) PublishPaymentEvent(ctx context.Context, evt domain.PaymentEvent) error {
	payload, err := json.Marshal(evt)
	if err != nil {
		return fmt.Errorf("marshal payment event: %w", err)
	}

	msg := kafkago.Message{
		Key:   []byte(evt.PaymentID), // route all events for the same payment to one partition
		Value: payload,
		Headers: []kafkago.Header{
			{Key: "event_type", Value: []byte(evt.EventType)},
			{Key: "tenant_id", Value: []byte(evt.TenantID)},
		},
	}

	if err := p.writer.WriteMessages(ctx, msg); err != nil {
		return fmt.Errorf("write payment event to kafka: %w", err)
	}

	p.log.Debug().
		Str("payment_id", evt.PaymentID).
		Str("event_type", evt.EventType).
		Str("status", string(evt.Status)).
		Msg("payment event published")

	return nil
}

// Close flushes pending messages and closes the underlying Kafka writer.
// Call this during graceful shutdown.
func (p *Publisher) Close() error {
	return p.writer.Close()
}
