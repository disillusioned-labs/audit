package consumer

import (
	"encoding/json"
	"fmt"
	"net/netip"

	"github.com/disillusioned-labs/audit/internal/service/audit"
	"github.com/disillusioned-labs/platform/kafka"
	"github.com/google/uuid"
)

const (
	headerEventID       = "event-id"
	headerEventVersion  = "event-version"
	headerSourceService = "source-service"
	headerAggregateType = "aggregate-type"
	headerAggregateID   = "aggregate-id"
)

type auditPayload struct {
	ActorID        string `json:"actor_id"`
	UserID         string `json:"user_id"`
	TenantID       string `json:"tenant_id"`
	OrganizationID string `json:"organization_id"`
	Status         string `json:"status"`
	IPAddress      string `json:"ip_address"`
	UserAgent      string `json:"user_agent"`
}

func decodeAuditEvent(
	record kafka.Record,
) (audit.CreateAuditEventInput, error) {
	if record.Topic == "" {
		return audit.CreateAuditEventInput{}, fmt.Errorf(
			"kafka record topic is empty",
		)
	}

	eventID, err := kafka.RequiredUUIDHeader(record.Headers, headerEventID)
	if err != nil {
		return audit.CreateAuditEventInput{}, err
	}

	eventVersion, err := kafka.RequiredIntHeader(record.Headers, headerEventVersion)
	if err != nil {
		return audit.CreateAuditEventInput{}, err
	}

	sourceService, err := kafka.RequiredHeader(
		record.Headers,
		headerSourceService,
	)
	if err != nil {
		return audit.CreateAuditEventInput{}, err
	}

	aggregateType, err := kafka.RequiredHeader(
		record.Headers,
		headerAggregateType,
	)
	if err != nil {
		return audit.CreateAuditEventInput{}, err
	}

	aggregateID, err := kafka.RequiredUUIDHeader(
		record.Headers,
		headerAggregateID,
	)
	if err != nil {
		return audit.CreateAuditEventInput{}, err
	}

	var payload auditPayload
	if err := json.Unmarshal(record.Value, &payload); err != nil {
		return audit.CreateAuditEventInput{}, fmt.Errorf(
			"unmarshal record value: %w",
			err,
		)
	}

	actorID := parseUUID(payload.ActorID)
	if actorID == nil {
		actorID = parseUUID(payload.UserID)
	}

	tenantID := parseUUID(payload.TenantID)
	if tenantID == nil {
		tenantID = parseUUID(payload.OrganizationID)
	}

	return audit.CreateAuditEventInput{
		EventID:       eventID,
		EventType:     record.Topic,
		EventVersion:  eventVersion,
		SourceService: sourceService,
		AggregateType: aggregateType,
		AggregateID:   aggregateID,

		ActorID:  actorID,
		TenantID: tenantID,
		Status:   optionalString(payload.Status),

		IPAddress: parseIP(payload.IPAddress),
		UserAgent: optionalString(payload.UserAgent),

		TraceID: kafka.OptionalHeader(record.Headers, "trace-id"),

		Details: record.Value,
	}, nil
}

func optionalString(value string) *string {
	if value == "" {
		return nil
	}

	return &value
}

func parseUUID(value string) *uuid.UUID {
	if value == "" {
		return nil
	}

	id, err := uuid.Parse(value)
	if err != nil {
		return nil
	}

	return &id
}

func parseIP(value string) *netip.Addr {
	if value == "" {
		return nil
	}

	ip, err := netip.ParseAddr(value)
	if err != nil {
		return nil
	}

	return &ip
}
