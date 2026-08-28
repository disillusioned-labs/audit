// Package app owns the application lifecycle: bootstrap infrastructure,
package app

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/disillusioned-labs/audit/internal/config"
	"github.com/disillusioned-labs/audit/internal/consumer"
	"github.com/disillusioned-labs/audit/internal/repository"
	"github.com/disillusioned-labs/audit/internal/service/audit"
	"github.com/disillusioned-labs/platform/kafka"
	"github.com/disillusioned-labs/platform/postgres"
	"github.com/disillusioned-labs/platform/retry"
	"github.com/disillusioned-labs/platform/telemetry"
	"go.opentelemetry.io/otel"
	"golang.org/x/sync/errgroup"

	migrations "github.com/disillusioned-labs/audit/db/migrations"
)

// otelFlushTimeout bounds the trace flush at exit: if the OTLP collector is
// unreachable, the batch exporter blocks indefinitely and the process never
// exits.
const otelFlushTimeout = 5 * time.Second

// RunConsumer boots the consumer process with the given configuration and blocks
// until the process is told to stop. The caller owns loading and validating cfg.
func RunConsumer(cfg *config.Config) error {
	ctx, stop := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
		syscall.SIGTERM,
	)
	defer stop()

	log := telemetry.NewLogger(
		cfg.Log.Level,
		telemetry.Format(cfg.Log.Format),
		telemetry.Env(cfg.Service.Env),
		telemetry.Service(cfg.Service.Name),
	)
	slog.SetDefault(log)

	log.Info(
		"starting",
		"service", cfg.Service.Name,
		"build", buildInfo(),
		"role", "consumer",
	)

	// -------------------------------------------------------------------------
	// Telemetry
	// -------------------------------------------------------------------------
	otelOpts := []telemetry.Option{
		telemetry.WithBuild(version, commit),
	}

	if cfg.OTel.TracesEnabled() {
		sampler, err := telemetry.NewSampler(
			cfg.OTel.TracesSampler,
			cfg.OTel.TracesSamplerArg,
		)
		if err != nil {
			return fmt.Errorf("configure trace sampler: %w", err)
		}

		otelOpts = append(
			otelOpts,
			telemetry.WithTracing(
				cfg.OTel.TraceEndpoint(),
				sampler,
			),
		)
	}

	if cfg.OTel.MetricsEnabled() {
		otelOpts = append(
			otelOpts,
			telemetry.WithMetrics(
				cfg.OTel.MetricEndpoint(),
				cfg.OTel.MetricExportInterval(),
			),
		)
	}

	shutdownOtel, err := telemetry.Setup(
		ctx,
		cfg.Service.Name,
		cfg.Service.Env,
		otelOpts...,
	)
	if err != nil {
		return fmt.Errorf("setup telemetry: %w", err)
	}

	log.Info(
		"telemetry configured",
		"traces", exportTarget(
			cfg.OTel.TracesEnabled(),
			cfg.OTel.TraceEndpoint(),
		),
		"metrics", exportTarget(
			cfg.OTel.MetricsEnabled(),
			cfg.OTel.MetricEndpoint(),
		),
		"metric_export_interval",
		cfg.OTel.MetricExportInterval(),
	)

	defer func() {
		flushCtx, cancel := context.WithTimeout(
			context.Background(),
			otelFlushTimeout,
		)
		defer cancel()

		if err := shutdownOtel(flushCtx); err != nil {
			log.Error("otel shutdown failed", "error", err)
		}
	}()

	// -------------------------------------------------------------------------
	// PostgreSQL
	// -------------------------------------------------------------------------
	pool, err := postgres.NewPool(
		ctx,
		cfg.Postgres.DSN,
		postgres.MaxConns(cfg.Postgres.MaxConns),
		postgres.MinConns(cfg.Postgres.MinConns),
		postgres.MaxConnLifetime(cfg.Postgres.MaxConnLifetime),
		postgres.QueryExecMode(cfg.Postgres.QueryExecMode),
	)
	if err != nil {
		return fmt.Errorf("connect postgres: %w", err)
	}
	defer pool.Close()

	log.Info("connected to postgres", "postgres", cfg.Postgres)

	if cfg.Postgres.Migrate {
		if err := postgres.Migrate(ctx, pool, migrations.FS, log); err != nil {
			return fmt.Errorf("run migrations: %w", err)
		}
	}
	repo := repository.NewStore(pool)

	// -------------------------------------------------------------------------
	// Kafka
	// -------------------------------------------------------------------------
	kafkaCfg := kafka.KafkaConfig{
		Brokers:     cfg.Kafka.Brokers,
		ClientID:    cfg.Kafka.ClientID,
		PingTimeout: cfg.Kafka.PingTimeout,
		Producer: kafka.ProducerConfig{
			RecordRetries:         cfg.Kafka.RecordRetries,
			RecordDeliveryTimeout: cfg.Kafka.RecordDeliveryTimeout,
		},
		Consumer: kafka.ConsumerConfig{
			Group:    cfg.Kafka.ConsumerGroup,
			Topics:   cfg.Kafka.ConsumerTopics,
			DLQTopic: cfg.Kafka.DLQTopic,
			Retry: kafka.RetryConfig{
				MaxAttempts:  cfg.Kafka.RetryMaxAttempts,
				InitialDelay: cfg.Kafka.RetryInitialDelay,
				MaxDelay:     cfg.Kafka.RetryMaxDelay,
			},
		},
	}

	kafkaClient, err := kafka.New(ctx, kafkaCfg)
	if err != nil {
		return fmt.Errorf("connect kafka: %w", err)
	}
	defer kafkaClient.Close()

	log.Info(
		"connected to kafka",
		"brokers", cfg.Kafka.Brokers,
		"client_id", cfg.Kafka.ClientID,
		"consumer_group", cfg.Kafka.ConsumerGroup,
		"topic", cfg.Kafka.ConsumerTopics,
		"topic_dlq", cfg.Kafka.DLQTopic,
	)

	kafkaConsumer := kafka.NewConsumer(kafkaClient)
	kafkaProducer := kafka.NewProducer(kafkaClient)
	dlqPublisher := kafka.NewDLQPublisher(kafkaProducer, kafkaCfg.Consumer.DLQTopic, log)

	// -------------------------------------------------------------------------
	// Metrics
	// -------------------------------------------------------------------------
	meter := otel.Meter("audit/consumer")

	consumerMetrics, err := consumer.NewConsumerMetrics(meter)
	if err != nil {
		return fmt.Errorf("create consumer metrics: %w", err)
	}

	// -------------------------------------------------------------------------
	// Retry
	// -------------------------------------------------------------------------
	retryPolicy := retry.RetryPolicy{
		MaxAttempts:  kafkaCfg.Consumer.Retry.MaxAttempts,
		InitialDelay: kafkaCfg.Consumer.Retry.InitialDelay,
		MaxDelay:     kafkaCfg.Consumer.Retry.MaxDelay,
	}

	if err := retryPolicy.Validate(); err != nil {
		return fmt.Errorf("validate consumer retry policy: %w", err)
	}

	// -------------------------------------------------------------------------
	// Service
	// -------------------------------------------------------------------------
	auditService := audit.NewAuditService(repo, log)

	// -------------------------------------------------------------------------
	// Consumer
	// -------------------------------------------------------------------------
	consumer := consumer.NewConsumer(
		kafkaConsumer,
		dlqPublisher,
		auditService,
		retryPolicy,
		consumerMetrics,
		log,
	)

	g, runCtx := errgroup.WithContext(ctx)

	g.Go(func() error {
		return consumer.Run(runCtx)
	})

	<-runCtx.Done()

	signalled := ctx.Err() != nil

	log.Info(
		"shutdown initiated",
		"cause", shutdownCause(signalled),
	)

	stop()

	if err := g.Wait(); err != nil {
		return err
	}

	log.Info("shutdown complete")

	return nil
}

func exportTarget(enabled bool, endpoint string) string {
	if !enabled {
		return "disabled"
	}
	return endpoint
}

func shutdownCause(signalled bool) string {
	if signalled {
		return "signal"
	}
	return "listener failure"
}
