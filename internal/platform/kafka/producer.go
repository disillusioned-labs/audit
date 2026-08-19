package kafka

import (
	"context"
	"fmt"

	"github.com/twmb/franz-go/pkg/kgo"
)

// Producer publishes records to Kafka.
type Producer interface {
	Publish(ctx context.Context, record Record) error
	Close()
}

// ClientProducer implements Producer using franz-go.
type ClientProducer struct {
	client *kgo.Client
}

// NewProducer creates a producer backed by an existing Kafka client.
//
// The Kafka client is shared so producers and consumers can use the same
// connection pool and metadata/cache.
func NewProducer(client *kgo.Client) *ClientProducer {
	return &ClientProducer{
		client: client,
	}
}

// Publish publishes one record and waits until Kafka acknowledges delivery.
//
// The caller's context controls cancellation. If the context is cancelled
// while the record is waiting for delivery, Publish returns the context error.
func (p *ClientProducer) Publish(
	ctx context.Context,
	record Record,
) error {
	if p == nil || p.client == nil {
		return fmt.Errorf("kafka producer is not initialized")
	}

	if record.Topic == "" {
		return fmt.Errorf("kafka topic must not be empty")
	}

	kafkaRecord := &kgo.Record{
		Topic:   record.Topic,
		Key:     record.Key,
		Value:   record.Value,
		Headers: record.Headers,
	}

	_, err := p.client.ProduceSync(ctx, kafkaRecord).First()
	if err != nil {
		return fmt.Errorf(
			"publish kafka record topic=%s: %w",
			record.Topic,
			err,
		)
	}

	return nil
}

// Close releases the underlying Kafka client.
//
// Normally the application should close the shared client rather than closing
// multiple producer wrappers independently.
func (p *ClientProducer) Close() {
	if p == nil || p.client == nil {
		return
	}

	p.client.Close()
}
