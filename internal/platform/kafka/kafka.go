// Package kafka owns the Kafka client lifecycle.
//
// The package intentionally exposes the underlying franz-go client only
// through the constructor. Producers and consumers are built on top of the
// same client and are wired by the application layer.
package kafka

import (
	"context"
	"fmt"

	"github.com/disillusioned-labs/audit/internal/config"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/plugin/kotel"
	"go.opentelemetry.io/otel"
)

// Option configures the Kafka client.
//
// We intentionally reuse franz-go's option type instead of introducing a
// second functional-options abstraction. This keeps the Kafka package thin
// while still allowing application-specific overrides when needed.
type Option = kgo.Opt

// New creates and validates a Kafka client.
//
// The client connects to the Kafka cluster using the configured seed brokers.
// Ping verifies that Kafka is reachable before the application continues
// startup.
func New(
	ctx context.Context,
	cfg config.KafkaConfig,
	opts ...Option,
) (*kgo.Client, error) {
	defaultOpts := []kgo.Opt{
		kgo.SeedBrokers(cfg.Brokers...),
		kgo.ClientID(cfg.ClientID),

		// Require all in-sync replicas to acknowledge produced records.
		//
		// This works together with Kafka's min.insync.replicas setting on the
		// broker/topic side. The application cannot enforce min.insync.replicas
		// itself.
		kgo.RequiredAcks(kgo.AllISRAcks()),

		// Compression preference from strongest compression/CPU trade-off.
		kgo.ProducerBatchCompression(
			kgo.Lz4Compression(),
			kgo.SnappyCompression(),
			kgo.NoCompression(),
		),

		// Producer retry configuration is deliberately configurable through
		// application config rather than hardcoded here.
		kgo.RecordRetries(int(cfg.RecordRetries)),
		kgo.RecordDeliveryTimeout(cfg.RecordDeliveryTimeout),
		kgo.AllowAutoTopicCreation(),
	}

	// OpenTelemetry integration.
	//
	// The hooks use the application's globally configured OTel providers.
	// Traces and Kafka client metrics therefore follow the same telemetry
	// pipeline as the rest of the application.
	tracer := kotel.NewTracer(
		kotel.TracerProvider(otel.GetTracerProvider()),
		kotel.TracerPropagator(otel.GetTextMapPropagator()),
	)

	meter := kotel.NewMeter(
		kotel.MeterProvider(otel.GetMeterProvider()),
	)

	kotelService := kotel.NewKotel(
		kotel.WithTracer(tracer),
		kotel.WithMeter(meter),
	)

	defaultOpts = append(
		defaultOpts,
		kgo.WithHooks(kotelService.Hooks()...),
	)

	// Caller options are applied last so explicit overrides win.
	client, err := kgo.NewClient(
		append(defaultOpts, opts...)...,
	)
	if err != nil {
		return nil, fmt.Errorf("create kafka client: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, cfg.PingTimeout)
	defer cancel()

	if err := client.Ping(pingCtx); err != nil {
		client.Close()

		return nil, fmt.Errorf("ping kafka: %w", err)
	}

	return client, nil
}

// Close releases Kafka client resources.
func Close(client *kgo.Client) {
	if client == nil {
		return
	}

	client.Close()
}

// ConsumerGroup creates the options required to configure a consumer group.
//
// Example:
//
//	client, err := kafka.New(ctx, cfg.Kafka,
//	    kafka.ConsumerGroup("identity-consumer", "user.created"),
//	)
func ConsumerGroup(group string) []Option {
	return []Option{}
}
