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

// Publisher implements webhook.EventPublisher and writes payment and subscription
// events to their respective Kafka topics in JSON format.
type Publisher struct {
	paymentWriter      *kafkago.Writer // payment.events topic
	subscriptionWriter *kafkago.Writer // subscription.events topic
	log                zerolog.Logger
}

// NewPublisher creates a Publisher connected to the given brokers.
// paymentTopic is cfg.Kafka.TopicPaymentEvents (default: "payment.events").
// subscriptionTopic is cfg.Kafka.TopicSubscriptionEvents (default: "subscription.events").
func NewPublisher(brokers []string, paymentTopic, subscriptionTopic string, log zerolog.Logger) *Publisher {
	// Short MetadataTTL ensures stale leader info (from before kafka-init ran)
	// is discarded quickly. Combined with 1 partition per topic on a single
	// broker, leader election races become impossible.
	transport := &kafkago.Transport{
		MetadataTTL: 2 * time.Second,
	}

	makeWriter := func(topic string) *kafkago.Writer {
		return &kafkago.Writer{
			Addr:  kafkago.TCP(brokers...),
			Topic: topic,
			// RoundRobin works correctly with any number of partitions and
			// does not require pre-fetching partition count like Hash does.
			Balancer:     &kafkago.RoundRobin{},
			Transport:    transport,
			BatchTimeout: 10 * time.Millisecond,
			// RequireOne is sufficient for a single-broker dev setup.
			RequiredAcks: kafkago.RequireOne,
			// WriteMessages calls partitions() before every batch, which
			// re-fetches metadata via the transport. MaxAttempts > 1 gives
			// the leader election window a chance to close between retries.
			MaxAttempts:            5,
			WriteBackoffMin:        500 * time.Millisecond,
			WriteBackoffMax:        2 * time.Second,
			AllowAutoTopicCreation: true,
			Async:                  false,
		}
	}

	return &Publisher{
		paymentWriter:      makeWriter(paymentTopic),
		subscriptionWriter: makeWriter(subscriptionTopic),
		log:                log.With().Str("component", "kafka_publisher").Logger(),
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
		Key:   []byte(evt.PaymentID),
		Value: payload,
		Headers: []kafkago.Header{
			{Key: "event_type", Value: []byte(evt.EventType)},
			{Key: "tenant_id", Value: []byte(evt.TenantID)},
		},
	}

	if err := p.paymentWriter.WriteMessages(ctx, msg); err != nil {
		return fmt.Errorf("write payment event to kafka: %w", err)
	}

	p.log.Debug().
		Str("payment_id", evt.PaymentID).
		Str("event_type", evt.EventType).
		Str("status", string(evt.Status)).
		Msg("payment event published")

	return nil
}

// PublishSubscriptionEvent serialises evt to JSON and writes it to the
// subscription.events topic.  Implements webhook.EventPublisher.
func (p *Publisher) PublishSubscriptionEvent(ctx context.Context, evt domain.SubscriptionEvent) error {
	payload, err := json.Marshal(evt)
	if err != nil {
		return fmt.Errorf("marshal subscription event: %w", err)
	}

	msg := kafkago.Message{
		Key:   []byte(evt.SubscriptionID),
		Value: payload,
		Headers: []kafkago.Header{
			{Key: "event_type", Value: []byte(evt.EventType)},
			{Key: "tenant_id", Value: []byte(evt.TenantID)},
			{Key: "member_id", Value: []byte(evt.MemberID)},
		},
	}

	if err := p.subscriptionWriter.WriteMessages(ctx, msg); err != nil {
		return fmt.Errorf("write subscription event to kafka: %w", err)
	}

	p.log.Debug().
		Str("subscription_id", evt.SubscriptionID).
		Str("event_type", evt.EventType).
		Str("status", string(evt.Status)).
		Msg("subscription event published")

	return nil
}

// Close flushes pending messages and closes the underlying Kafka writers.
// Call this during graceful shutdown.
func (p *Publisher) Close() error {
	payErr := p.paymentWriter.Close()
	subErr := p.subscriptionWriter.Close()
	if payErr != nil {
		return payErr
	}
	return subErr
}
