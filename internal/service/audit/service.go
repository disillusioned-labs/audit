package audit

import (
	"context"
	"log/slog"

	"github.com/disillusioned-labs/audit/internal/repository"
	"github.com/disillusioned-labs/audit/internal/service"
	"github.com/jackc/pgx/v5/pgtype"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

var tracer = otel.Tracer("service/audit")

type AuditService interface {
	Create(ctx context.Context, input CreateAuditEventInput) error
}

type auditService struct {
	repo repository.Store
	log  *slog.Logger
}

func NewAuditService(
	repo repository.Store,
	log *slog.Logger,
) AuditService {
	return &auditService{
		repo: repo,
		log:  log,
	}
}

func (s *auditService) Create(
	ctx context.Context,
	input CreateAuditEventInput,
) error {
	ctx, span := tracer.Start(ctx, "AuditService.Create")
	defer span.End()

	span.SetAttributes(
		attribute.String("audit_event.event_id", input.EventID.String()),
		attribute.String("audit_event.event_type", input.EventType),
		attribute.Int("audit_event.event_version", input.EventVersion),
		attribute.String("audit_event.source_service", input.SourceService),
		attribute.String("audit_event.aggregate_type", input.AggregateType),
		attribute.String("audit_event.aggregate_id", input.AggregateID.String()),
	)

	if input.ActorType != nil {
		span.SetAttributes(
			attribute.String("audit_event.actor_type", *input.ActorType),
		)
	}

	if input.ActorID != nil {
		span.SetAttributes(
			attribute.String("audit_event.actor_id", input.ActorID.String()),
		)
	}

	if input.TenantID != nil {
		span.SetAttributes(
			attribute.String("audit_event.tenant_id", input.TenantID.String()),
		)
	}

	if input.Status != nil {
		span.SetAttributes(
			attribute.String("audit_event.status", *input.Status),
		)
	}

	err := s.repo.ExecTx(ctx, func(q repository.Querier) error {
		processed, err := q.IsEventProcessed(ctx, input.EventID)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(
				codes.Error,
				"check processed event failed",
			)

			s.log.ErrorContext(
				ctx,
				"check processed event failed",
				"error", err,
				"event_id", input.EventID,
				"event_type", input.EventType,
			)

			return err
		}

		if processed {
			span.SetAttributes(
				attribute.Bool("audit_event.already_processed", true),
			)

			s.log.DebugContext(
				ctx,
				"audit event already processed",
				"event_id", input.EventID,
				"event_type", input.EventType,
			)

			return nil
		}

		_, err = q.CreateProcessedEvent(
			ctx,
			repository.CreateProcessedEventParams{
				EventID:      input.EventID,
				EventType:    input.EventType,
				EventVersion: int32(input.EventVersion),
			},
		)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(
				codes.Error,
				"create processed event failed",
			)

			s.log.ErrorContext(
				ctx,
				"create processed event failed",
				"error", err,
				"event_id", input.EventID,
				"event_type", input.EventType,
			)

			return err
		}

		_, err = q.CreateAuditEvent(
			ctx,
			repository.CreateAuditEventParams{
				EventID:       input.EventID,
				EventType:     input.EventType,
				EventVersion:  int32(input.EventVersion),
				SourceService: input.SourceService,

				ActorType: nullableText(input.ActorType),
				ActorID:   input.ActorID,

				AggregateType: input.AggregateType,
				AggregateID:   input.AggregateID,

				TenantID: input.TenantID,
				Status:   nullableText(input.Status),

				IpAddress: input.IPAddress,
				UserAgent: nullableText(input.UserAgent),

				TraceID: nullableText(input.TraceID),
				Details: input.Details,
			},
		)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(
				codes.Error,
				"create audit event failed",
			)

			s.log.ErrorContext(
				ctx,
				"create audit event failed",
				"error", err,
				"event_id", input.EventID,
				"event_type", input.EventType,
				"source_service", input.SourceService,
				"aggregate_type", input.AggregateType,
				"aggregate_id", input.AggregateID,
			)

			return err
		}

		return nil
	})

	if err != nil {
		span.RecordError(err)
		span.SetStatus(
			codes.Error,
			"create audit event transaction failed",
		)

		s.log.ErrorContext(
			ctx,
			"create audit event transaction failed",
			"error", err,
			"event_id", input.EventID,
			"event_type", input.EventType,
			"source_service", input.SourceService,
			"aggregate_type", input.AggregateType,
			"aggregate_id", input.AggregateID,
		)

		return service.ErrInternal
	}

	s.log.DebugContext(
		ctx,
		"audit event created",
		"event_id", input.EventID,
		"event_type", input.EventType,
		"source_service", input.SourceService,
		"aggregate_type", input.AggregateType,
		"aggregate_id", input.AggregateID,
	)

	return nil
}

func nullableText(value *string) pgtype.Text {
	if value == nil {
		return pgtype.Text{}
	}

	return pgtype.Text{
		String: *value,
		Valid:  true,
	}
}
