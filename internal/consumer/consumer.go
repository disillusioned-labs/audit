package consumer

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/disillusioned-labs/audit/internal/service/audit"
	"github.com/disillusioned-labs/platform/kafka"
	"github.com/disillusioned-labs/platform/retry"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

var tracer = otel.Tracer("consumer/audit")

const eventTypeHeader = "event-type"

type Consumer struct {
	kafkaConsumer *kafka.Consumer
	dlqPublisher  *kafka.DLQPublisher
	auditService  audit.AuditService
	retryPolicy   retry.RetryPolicy
	metrics       *ConsumerMetrics
	log           *slog.Logger
}

func NewConsumer(
	kafkaConsumer *kafka.Consumer,
	dlqPublisher *kafka.DLQPublisher,
	auditService audit.AuditService,
	retryPolicy retry.RetryPolicy,
	metrics *ConsumerMetrics,
	log *slog.Logger,
) *Consumer {
	return &Consumer{
		kafkaConsumer: kafkaConsumer,
		dlqPublisher:  dlqPublisher,
		auditService:  auditService,
		retryPolicy:   retryPolicy,
		metrics:       metrics,
		log:           log,
	}
}

func (c *Consumer) Run(ctx context.Context) error {
	if c == nil {
		return fmt.Errorf("consumer is nil")
	}

	if c.kafkaConsumer == nil {
		return fmt.Errorf("kafka consumer is nil")
	}

	if c.log == nil {
		return fmt.Errorf("consumer logger is nil")
	}

	if err := c.retryPolicy.Validate(); err != nil {
		return fmt.Errorf("validate retry policy: %w", err)
	}

	c.log.Info("consumer started")

	for {
		records, err := c.kafkaConsumer.Poll(ctx)
		if err != nil {
			if ctx.Err() != nil {
				c.commitPending()
				return nil
			}

			return fmt.Errorf("poll kafka: %w", err)
		}

		for _, record := range records {
			if err := c.processWithRetry(recordContext(ctx, record), record); err != nil {
				c.commitPending()
				return fmt.Errorf(
					"process kafka record topic=%s partition=%d offset=%d: %w",
					record.Topic,
					record.Partition,
					record.Offset,
					err,
				)
			}

			if err := c.kafkaConsumer.CommitRecords(ctx, record); err != nil {
				if c.metrics != nil {
					c.metrics.recordCommitFailed(
						ctx,
						record.Topic,
					)
				}

				c.commitPending()
				return fmt.Errorf(
					"commit kafka record topic=%s partition=%d offset=%d: %w",
					record.Topic,
					record.Partition,
					record.Offset,
					err,
				)
			}
		}
	}
}

// commitPending flushes any processed-but-uncommitted offsets to the broker.
// It uses a fresh context because the caller's context is already cancelled.
func (c *Consumer) commitPending() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := c.kafkaConsumer.CommitUncommitted(ctx); err != nil {
		c.log.Error("failed to commit pending offsets during shutdown", "error", err)
	}
}

func (c *Consumer) processWithRetry(
	ctx context.Context,
	record kafka.Record,
) error {
	ctx, span := tracer.Start(ctx, "audit.consumer.process")
	defer span.End()

	start := time.Now()
	eventType := recordEventType(record)

	span.SetAttributes(
		attribute.String("messaging.system", "kafka"),
		attribute.String("messaging.destination.name", record.Topic),
		attribute.Int64("messaging.kafka.partition", int64(record.Partition)),
		attribute.Int64("messaging.kafka.offset", record.Offset),
		attribute.String("event.type", eventType),
	)

	defer func() {
		if c.metrics != nil {
			c.metrics.recordProcessingDuration(
				ctx,
				start,
				record.Topic,
				eventType,
			)
		}
	}()

	var lastErr error

	for attempt := 1; attempt <= c.retryPolicy.MaxAttempts; attempt++ {
		err := c.processRecord(ctx, record)
		if err == nil {
			if c.metrics != nil {
				c.metrics.recordProcessed(
					ctx,
					record.Topic,
					eventType,
				)
			}

			return nil
		}

		lastErr = err
		errType := classifyError(err)

		if IsPermanent(err) {
			if c.metrics != nil {
				c.metrics.recordFailed(
					ctx,
					record.Topic,
					eventType,
					errType,
				)
			}

			if err := c.moveToDLQ(
				ctx,
				record,
				err,
				attempt,
			); err != nil {
				span.RecordError(err)
				span.SetStatus(
					codes.Error,
					"move record to dlq failed",
				)

				return err
			}

			if c.metrics != nil {
				c.metrics.recordDLQ(
					ctx,
					record.Topic,
					eventType,
					errType,
				)
			}

			return nil
		}

		if attempt == c.retryPolicy.MaxAttempts {
			break
		}

		if c.metrics != nil {
			c.metrics.recordRetried(
				ctx,
				record.Topic,
				eventType,
				errType,
			)
		}

		c.log.Warn(
			"retrying kafka record",
			"topic", record.Topic,
			"partition", record.Partition,
			"offset", record.Offset,
			"attempt", attempt+1,
			"max_attempts", c.retryPolicy.MaxAttempts,
			"error", err,
		)

		if err := c.retryPolicy.Wait(ctx, attempt); err != nil {
			span.RecordError(err)
			span.SetStatus(
				codes.Error,
				"retry wait failed",
			)

			return err
		}
	}

	errType := classifyError(lastErr)

	if c.metrics != nil {
		c.metrics.recordFailed(
			ctx,
			record.Topic,
			eventType,
			errType,
		)
	}

	if err := c.moveToDLQ(
		ctx,
		record,
		lastErr,
		c.retryPolicy.MaxAttempts,
	); err != nil {
		span.RecordError(err)
		span.SetStatus(
			codes.Error,
			"move record to dlq failed",
		)

		return err
	}

	if c.metrics != nil {
		c.metrics.recordDLQ(
			ctx,
			record.Topic,
			eventType,
			errType,
		)
	}

	return nil
}

func (c *Consumer) moveToDLQ(
	ctx context.Context,
	record kafka.Record,
	cause error,
	attempt int,
) error {
	if cause == nil {
		return fmt.Errorf("move kafka record to dlq: cause is nil")
	}

	metadata := kafka.DLQMetadata{
		SourceTopic:     record.Topic,
		SourcePartition: record.Partition,
		SourceOffset:    record.Offset,
		Attempt:         attempt,
		ErrorType:       "transient",
		ErrorCode:       "PROCESSING_FAILED",
	}

	if IsPermanent(cause) {
		metadata.ErrorType = "permanent"
		metadata.ErrorCode = "PERMANENT_PROCESSING_ERROR"
	}

	if err := c.dlqPublisher.Publish(
		ctx,
		record,
		metadata,
	); err != nil {
		return fmt.Errorf(
			"move kafka record to dlq: %w",
			err,
		)
	}

	return nil
}

func (c *Consumer) processRecord(
	ctx context.Context,
	record kafka.Record,
) error {
	input, err := decodeAuditEvent(record)
	if err != nil {
		return Permanent(
			fmt.Errorf(
				"decode audit event: %w",
				err,
			),
		)
	}

	if err := c.auditService.Create(ctx, input); err != nil {
		return fmt.Errorf(
			"create audit event: %w",
			err,
		)
	}

	return nil
}

func recordEventType(record kafka.Record) string {
	value, ok := kafka.HeaderString(
		record.Headers,
		eventTypeHeader,
	)
	if !ok || value == "" {
		return "unknown"
	}

	return value
}

// recordContext continues the trace the producer injected into the record
// headers; records without one fall back to the poll loop's context.
func recordContext(ctx context.Context, record kafka.Record) context.Context {
	if record.Context != nil {
		return record.Context
	}
	return ctx
}
