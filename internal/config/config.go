// Package config loads and validates application configuration from environment
// variables (e.g. SERVER_PORT=9090), with a local .env file as a development
// convenience and production-safe defaults underneath.
//
// Variable names are unprefixed, sharing a namespace with everything else in
// the environment. "service" is named defensively so it does not collide with
// generic NAME/ENV; "otel" deliberately does the opposite and adopts the
// OpenTelemetry SDK's own spelling (see OTelConfig). Check any new key against
// what an orchestrator might already set (Kubernetes injects <SERVICE>_PORT
// for every Service in the namespace).
//
// .env.example is the single list of available settings, written exactly as
// a deployment sets them - there is deliberately one vocabulary, not a
// config.yaml shadowing the env vars.
package config

import (
	"errors"
	"fmt"
	"strings"

	platformconfig "github.com/disillusioned-labs/platform/config"
	"github.com/spf13/viper"
)

// Config is the root of all application settings, one field per subsystem.
type Config struct {
	Service  platformconfig.ServiceConfig  `mapstructure:"service"`
	Postgres platformconfig.PostgresConfig `mapstructure:"postgres"`
	Kafka    platformconfig.KafkaConfig    `mapstructure:"kafka"`
	OTel     platformconfig.OTelConfig     `mapstructure:"otel"`
	Log      platformconfig.LogConfig      `mapstructure:"log"`
}

// DotEnvFile is the optional local overrides file, loaded from the working
// directory. It is git-ignored; .env.example documents every key.
const DotEnvFile = ".env"

// Load builds the configuration from environment variables (e.g. POSTGRES_DSN),
// falling back to the defaults in setDefaults.
//
// A .env file in the working directory is loaded into the environment first as
// a local development convenience; real environment variables always win, so
// the precedence is environment > .env > defaults. Deployments set variables
// directly and ship no file.
func Load() (*Config, error) {
	dotEnv, err := platformconfig.ParseDotEnv(DotEnvFile)
	if err != nil {
		return nil, err
	}

	v := viper.New()
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	setDefaults(v)

	// Layer .env in as defaults rather than by setting process environment
	// variables. Viper resolves AutomaticEnv before defaults, so a real
	// environment variable still wins for free - and Load leaves no global
	// state behind, which keeps it idempotent and safe to call from tests.
	for _, key := range v.AllKeys() {
		if value, ok := dotEnv[platformconfig.EnvKey(key)]; ok {
			v.SetDefault(key, value)
		}
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}
	cfg.Kafka.Brokers = platformconfig.NormalizeKafkaBrokers(cfg.Kafka.Brokers)
	cfg.Kafka.Consumer.Topics = platformconfig.NormalizeKafkaTopics(cfg.Kafka.Consumer.Topics)
	cfg.Service.InstanceID = platformconfig.InstanceID()
	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}
	return &cfg, nil
}

// validate rejects every value the app would otherwise silently misinterpret.
// A boilerplate is copied far more often than it is read, so an unset or
// fat-fingered override must fail at boot rather than degrade in production.
func (c *Config) validate() error {
	var errs []error
	fail := func(format string, args ...any) {
		errs = append(errs, fmt.Errorf(format, args...))
	}

	if err := platformconfig.ValidateService(&c.Service); err != nil {
		errs = append(errs, err)
	}
	if err := platformconfig.ValidatePostgres(&c.Postgres); err != nil {
		errs = append(errs, err)
	}
	if err := platformconfig.ValidateKafka(&c.Kafka); err != nil {
		errs = append(errs, err)
	}

	// Audit-specific: consumer fields.
	if strings.TrimSpace(c.Kafka.Consumer.Group) == "" {
		fail("kafka.consumer.group must not be empty")
	}

	seen := make(map[string]struct{}, len(c.Kafka.Consumer.Topics))

	for i, topic := range c.Kafka.Consumer.Topics {
		topic = strings.TrimSpace(topic)

		if topic == "" {
			fail("kafka.consumer.topics[%d] must not be empty", i)
			continue
		}

		if _, exists := seen[topic]; exists {
			fail("kafka.consumer.topics[%d] is duplicated: %q", i, topic)
		}

		seen[topic] = struct{}{}

		if topic == c.Kafka.Consumer.DLQTopic {
			fail(
				"kafka.consumer.topics[%d] must be different from kafka.consumer.dlq_topic",
				i,
			)
		}
	}

	if strings.TrimSpace(c.Kafka.Consumer.DLQTopic) == "" {
		fail("kafka.consumer.dlq_topic must not be empty")
	}

	retry := c.Kafka.Consumer.Retry

	if retry.MaxAttempts < 1 {
		fail("kafka.consumer.retry.max_attempts must be >= 1, got %d", retry.MaxAttempts)
	}

	if retry.InitialDelay <= 0 {
		fail("kafka.consumer.retry.initial_delay must be > 0, got %s", retry.InitialDelay)
	}

	if retry.MaxDelay <= 0 {
		fail("kafka.consumer.retry.max_delay must be > 0, got %s", retry.MaxDelay)
	}

	if retry.MaxDelay < retry.InitialDelay {
		fail("kafka.consumer.retry.max_delay (%s) must be >= kafka.consumer.retry.initial_delay (%s)",
			retry.MaxDelay, retry.InitialDelay)
	}

	// Audit-specific: producer fields.
	if c.Kafka.Producer.RecordRetries < 0 {
		fail(
			"kafka.producer.record_retries must be >= 0, got %d",
			c.Kafka.Producer.RecordRetries,
		)
	}

	if c.Kafka.Producer.RecordDeliveryTimeout <= 0 {
		fail(
			"kafka.producer.record_delivery_timeout must be > 0, got %s",
			c.Kafka.Producer.RecordDeliveryTimeout,
		)
	}

	if err := platformconfig.ValidateOTel(&c.OTel); err != nil {
		errs = append(errs, err)
	}
	if err := platformconfig.ValidateLog(&c.Log); err != nil {
		errs = append(errs, err)
	}

	return errors.Join(errs...)
}

// setDefaults registers every key with its production-safe value; Viper's
// AutomaticEnv only resolves keys it already knows, so an unregistered key
// would be invisible even when its variable is set.
func setDefaults(v *viper.Viper) {
	v.SetDefault("service.name", "audit")
	v.SetDefault("service.env", platformconfig.EnvDevelopment)

	v.SetDefault("postgres.dsn", "postgres://app:app@localhost:5433/app?sslmode=disable")
	v.SetDefault("postgres.max_conns", 25)
	v.SetDefault("postgres.min_conns", 2)
	v.SetDefault("postgres.max_conn_lifetime", "1h")
	v.SetDefault("postgres.migrate", false)
	v.SetDefault("postgres.query_exec_mode", "cache_statement")

	v.SetDefault("kafka.brokers", []string{"localhost:9092"})
	v.SetDefault("kafka.client_id", "audit")
	v.SetDefault("kafka.ping_timeout", "5s")
	v.SetDefault("kafka.producer.record_retries", int64(5))
	v.SetDefault("kafka.producer.record_delivery_timeout", "30s")
	v.SetDefault("kafka.consumer.group", "audit-consumer-group")
	v.SetDefault("kafka.consumer.topics", "audit")
	v.SetDefault("kafka.consumer.dlq_topic", "audit.dlq")
	v.SetDefault("kafka.consumer.retry.max_attempts", 3)
	v.SetDefault("kafka.consumer.retry.initial_delay", "1s")
	v.SetDefault("kafka.consumer.retry.max_delay", "30s")

	v.SetDefault("otel.sdk_disabled", false)
	v.SetDefault("otel.traces_exporter", platformconfig.OTelExporterOTLP)
	v.SetDefault("otel.metrics_exporter", platformconfig.OTelExporterOTLP)
	v.SetDefault("otel.exporter_otlp_endpoint", "http://localhost:4317")
	// Empty means "inherit the base endpoint"; registered anyway because
	// AutomaticEnv only resolves keys Viper already knows.
	v.SetDefault("otel.exporter_otlp_traces_endpoint", "")
	v.SetDefault("otel.exporter_otlp_metrics_endpoint", "")
	v.SetDefault("otel.traces_sampler", "parentbased_traceidratio")
	v.SetDefault("otel.traces_sampler_arg", 1.0)
	// Milliseconds, per spec - this is the OTel SDK's own default.
	v.SetDefault("otel.metric_export_interval", 60000)

	v.SetDefault("log.level", "info")
	v.SetDefault("log.format", "json")

}
