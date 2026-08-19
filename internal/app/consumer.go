// Package app owns the application lifecycle: bootstrap infrastructure,
// wire dependencies (see di.go), serve, and shut down gracefully.
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
	"github.com/disillusioned-labs/audit/internal/platform/kafka"
	"github.com/disillusioned-labs/audit/internal/platform/postgres"
	"github.com/disillusioned-labs/audit/internal/platform/telemetry"
	"github.com/disillusioned-labs/audit/internal/repository"
	"github.com/disillusioned-labs/audit/internal/service/audit"
	"github.com/twmb/franz-go/pkg/kgo"
	"golang.org/x/sync/errgroup"
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
		if err := postgres.Migrate(ctx, pool, log); err != nil {
			return fmt.Errorf("run migrations: %w", err)
		}
	}
	repo := repository.NewStore(pool)

	// -------------------------------------------------------------------------
	// Kafka
	// -------------------------------------------------------------------------
	kafkaOpts := []kafka.Option{
		kgo.ConsumerGroup(cfg.Kafka.ConsumerGroup),
		kgo.ConsumeTopics(".*"),
		kgo.ConsumeRegex(),
	}

	kafkaClient, err := kafka.New(
		ctx,
		cfg.Kafka,
		kafkaOpts...,
	)
	if err != nil {
		return fmt.Errorf("connect kafka: %w", err)
	}
	defer kafkaClient.Close()

	log.Info(
		"connected to kafka",
		"brokers", cfg.Kafka.Brokers,
		"client_id", cfg.Kafka.ClientID,
		"consumer_group", cfg.Kafka.ConsumerGroup,
	)

	kafkaConsumer := kafka.NewConsumer(kafkaClient)

	// -------------------------------------------------------------------------
	// Service
	// -------------------------------------------------------------------------
	auditService := audit.NewAuditService(repo, log)

	// -------------------------------------------------------------------------
	// Run
	// -------------------------------------------------------------------------
	g, runCtx := errgroup.WithContext(ctx)

	g.Go(func() error {
		for {
			fetches := kafkaConsumer.Poll(runCtx)

			if fetches.IsClientClosed() {
				log.Info("kafka consumer closed")
				return nil
			}

			if err := fetches.Err0(); err != nil {
				log.Error(
					"kafka poll failed",
					"error", err,
				)

				return fmt.Errorf("poll kafka: %w", err)
			}

			var processed []*kgo.Record

			fetches.EachRecord(func(record *kgo.Record) {
				log.Info(
					"kafka record received",
					"topic", record.Topic,
					"partition", record.Partition,
					"offset", record.Offset,
					"key", string(record.Key),
				)

				input, err := audit.MapKafkaRecordToCreateAuditEventInput(
					record,
				)
				if err != nil {
					log.Error(
						"map kafka record failed",
						"error", err,
						"topic", record.Topic,
						"partition", record.Partition,
						"offset", record.Offset,
					)

					// TODO: publish record to DLQ.
					return
				}

				log.Info(
					"kafka record mapped",
					"event_id", input.EventID,
					"event_type", input.EventType,
					"event_version", input.EventVersion,
					"source_service", input.SourceService,
					"aggregate_type", input.AggregateType,
					"aggregate_id", input.AggregateID,
				)

				if err := auditService.Create(runCtx, input); err != nil {
					log.Error(
						"create audit event failed",
						"error", err,
						"event_id", input.EventID,
						"event_type", input.EventType,
						"aggregate_type", input.AggregateType,
						"aggregate_id", input.AggregateID,
						"topic", record.Topic,
						"partition", record.Partition,
						"offset", record.Offset,
					)

					// TODO: publish record to DLQ.
					return
				}

				log.Info(
					"audit event created",
					"event_id", input.EventID,
					"event_type", input.EventType,
					"aggregate_type", input.AggregateType,
					"aggregate_id", input.AggregateID,
				)

				processed = append(processed, record)
			})

			if len(processed) == 0 {
				continue
			}

			if err := kafkaConsumer.CommitRecords(
				runCtx,
				processed...,
			); err != nil {
				log.Error(
					"commit kafka records failed",
					"error", err,
					"records", len(processed),
				)

				return fmt.Errorf(
					"commit processed kafka records: %w",
					err,
				)
			}

			log.Info(
				"kafka records committed",
				"records", len(processed),
			)
		}
	})

	<-runCtx.Done()

	signalled := ctx.Err() != nil

	log.Info(
		"shutdown initiated",
		"cause", shutdownCause(signalled),
	)

	stop()

	// Worker.Run observes runCtx cancellation and exits gracefully.
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
