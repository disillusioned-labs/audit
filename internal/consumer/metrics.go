package consumer

import (
	"context"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

const (
	metricAttrTopic     = "messaging.destination.name"
	metricAttrEventType = "event.type"
	metricAttrErrorType = "error.type"
)

type errorType string

const (
	errorTypePermanent errorType = "permanent"
	errorTypeTransient errorType = "transient"
	errorTypeUnknown   errorType = "unknown"
)

type ConsumerMetrics struct {
	recordsProcessed metric.Int64Counter
	recordsFailed    metric.Int64Counter
	recordsRetried   metric.Int64Counter
	recordsDLQ       metric.Int64Counter
	commitFailed     metric.Int64Counter

	processingDuration metric.Float64Histogram
}

func NewConsumerMetrics(
	meter metric.Meter,
) (*ConsumerMetrics, error) {
	recordsProcessed, err := meter.Int64Counter(
		"kafka.consumer.records.processed",
		metric.WithDescription(
			"Number of Kafka records successfully processed.",
		),
		metric.WithUnit("{record}"),
	)
	if err != nil {
		return nil, err
	}

	recordsFailed, err := meter.Int64Counter(
		"kafka.consumer.records.failed",
		metric.WithDescription(
			"Number of Kafka records that ultimately failed processing.",
		),
		metric.WithUnit("{record}"),
	)
	if err != nil {
		return nil, err
	}

	recordsRetried, err := meter.Int64Counter(
		"kafka.consumer.records.retried",
		metric.WithDescription(
			"Number of Kafka record retry attempts.",
		),
		metric.WithUnit("{retry}"),
	)
	if err != nil {
		return nil, err
	}

	recordsDLQ, err := meter.Int64Counter(
		"kafka.consumer.records.dlq",
		metric.WithDescription(
			"Number of Kafka records successfully published to the DLQ.",
		),
		metric.WithUnit("{record}"),
	)
	if err != nil {
		return nil, err
	}

	commitFailed, err := meter.Int64Counter(
		"kafka.consumer.commit.failed",
		metric.WithDescription(
			"Number of Kafka offset commit failures.",
		),
		metric.WithUnit("{error}"),
	)
	if err != nil {
		return nil, err
	}

	processingDuration, err := meter.Float64Histogram(
		"kafka.consumer.processing.duration",
		metric.WithDescription(
			"Time spent processing a Kafka record.",
		),
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, err
	}

	return &ConsumerMetrics{
		recordsProcessed:   recordsProcessed,
		recordsFailed:      recordsFailed,
		recordsRetried:     recordsRetried,
		recordsDLQ:         recordsDLQ,
		commitFailed:       commitFailed,
		processingDuration: processingDuration,
	}, nil
}

func (m *ConsumerMetrics) recordProcessed(
	ctx context.Context,
	topic string,
	eventType string,
) {
	if m == nil {
		return
	}

	m.recordsProcessed.Add(
		ctx,
		1,
		metric.WithAttributes(
			attribute.String(metricAttrTopic, topic),
			attribute.String(metricAttrEventType, eventType),
		),
	)
}

func (m *ConsumerMetrics) recordFailed(
	ctx context.Context,
	topic string,
	eventType string,
	errType errorType,
) {
	if m == nil {
		return
	}

	m.recordsFailed.Add(
		ctx,
		1,
		metric.WithAttributes(
			attribute.String(metricAttrTopic, topic),
			attribute.String(metricAttrEventType, eventType),
			attribute.String(metricAttrErrorType, string(errType)),
		),
	)
}

func (m *ConsumerMetrics) recordRetried(
	ctx context.Context,
	topic string,
	eventType string,
	errType errorType,
) {
	if m == nil {
		return
	}

	m.recordsRetried.Add(
		ctx,
		1,
		metric.WithAttributes(
			attribute.String(metricAttrTopic, topic),
			attribute.String(metricAttrEventType, eventType),
			attribute.String(metricAttrErrorType, string(errType)),
		),
	)
}

func (m *ConsumerMetrics) recordDLQ(
	ctx context.Context,
	topic string,
	eventType string,
	errType errorType,
) {
	if m == nil {
		return
	}

	m.recordsDLQ.Add(
		ctx,
		1,
		metric.WithAttributes(
			attribute.String(metricAttrTopic, topic),
			attribute.String(metricAttrEventType, eventType),
			attribute.String(metricAttrErrorType, string(errType)),
		),
	)
}

func (m *ConsumerMetrics) recordCommitFailed(
	ctx context.Context,
	topic string,
) {
	if m == nil {
		return
	}

	m.commitFailed.Add(
		ctx,
		1,
		metric.WithAttributes(
			attribute.String(metricAttrTopic, topic),
		),
	)
}

func (m *ConsumerMetrics) recordProcessingDuration(
	ctx context.Context,
	start time.Time,
	topic string,
	eventType string,
) {
	if m == nil {
		return
	}

	m.processingDuration.Record(
		ctx,
		time.Since(start).Seconds(),
		metric.WithAttributes(
			attribute.String(metricAttrTopic, topic),
			attribute.String(metricAttrEventType, eventType),
		),
	)
}
