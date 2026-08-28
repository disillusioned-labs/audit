package consumer

import (
	"encoding/json"
	"fmt"
	"net/netip"

	"github.com/disillusioned-labs/audit/internal/service/audit"
	"github.com/disillusioned-labs/platform/kafka"
)

const (
	headerEventID       = "event-id"
	headerEventVersion  = "event-version"
	headerSourceService = "source-service"
	headerAggregateType = "aggregate-type"
	headerAggregateID   = "aggregate-id"
)

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

	var recordData map[string]string
	if err := json.Unmarshal(record.Value, &recordData); err != nil {
		return audit.CreateAuditEventInput{}, fmt.Errorf(
			"unmarshal record value: %w",
			err,
		)
	}

	return audit.CreateAuditEventInput{
		EventID:       eventID,
		EventType:     record.Topic,
		EventVersion:  eventVersion,
		SourceService: sourceService,
		AggregateType: aggregateType,
		AggregateID:   aggregateID,

		IPAddress: parseIP(recordData["ip_address"]),
		UserAgent: optionalString(recordData["user_agent"]),

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
