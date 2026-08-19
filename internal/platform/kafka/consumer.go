package kafka

import (
	"context"
	"fmt"

	"github.com/twmb/franz-go/pkg/kgo"
)

// ConsumerHandler handles one Kafka record.
//
// Returning nil means the record was processed successfully.
// Returning an error stops processing the current batch and leaves offset
// commit decisions to the consumer/application layer.
type ConsumerHandler func(
	ctx context.Context,
	record *kgo.Record,
) error

// Consumer wraps franz-go's polling consumer.
type Consumer struct {
	client *kgo.Client
}

// NewConsumer creates a consumer using an existing Kafka client.
//
// Consumer-group configuration should normally be supplied when constructing
// the Kafka client.
func NewConsumer(client *kgo.Client) *Consumer {
	return &Consumer{
		client: client,
	}
}

// Poll fetches the next batch of Kafka records.
//
// This is intentionally a low-level helper. Business processing, database
// transactions, event-processing idempotency, DLQ policy, and offset commit
// decisions belong to the consumer/application layer.
func (c *Consumer) Poll(ctx context.Context) kgo.Fetches {
	if c == nil || c.client == nil {
		return nil
	}

	return c.client.PollFetches(ctx)
}

// Each iterates through fetched records.
//
// Processing stops after the first handler error. This helper does not commit
// offsets because offsets must only be committed after durable processing has
// succeeded.
func (c *Consumer) Each(
	ctx context.Context,
	fetches kgo.Fetches,
	handler ConsumerHandler,
) error {
	if c == nil || c.client == nil {
		return fmt.Errorf("kafka consumer is not initialized")
	}

	if handler == nil {
		return fmt.Errorf("kafka consumer handler must not be nil")
	}

	var firstErr error

	fetches.EachRecord(func(record *kgo.Record) {
		if firstErr != nil {
			return
		}

		if err := handler(ctx, record); err != nil {
			firstErr = fmt.Errorf(
				"process kafka record topic=%s partition=%d offset=%d: %w",
				record.Topic,
				record.Partition,
				record.Offset,
				err,
			)
		}
	})

	return firstErr
}

// CommitRecords commits offsets for successfully processed records.
//
// The application should call this only after durable processing has
// succeeded.
func (c *Consumer) CommitRecords(
	ctx context.Context,
	records ...*kgo.Record,
) error {
	if c == nil || c.client == nil {
		return fmt.Errorf("kafka consumer is not initialized")
	}

	if len(records) == 0 {
		return nil
	}

	if err := c.client.CommitRecords(ctx, records...); err != nil {
		return fmt.Errorf("commit kafka offsets: %w", err)
	}

	return nil
}

// Close releases the underlying Kafka client.
func (c *Consumer) Close() {
	if c == nil || c.client == nil {
		return
	}

	c.client.Close()
}
