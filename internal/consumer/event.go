package consumer

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"strconv"

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

func decodeAuditEvent(
	record kafka.Record,
) (audit.CreateAuditEventInput, error) {
	if record.Topic == "" {
		return audit.CreateAuditEventInput{}, fmt.Errorf(
			"kafka record topic is empty",
		)
	}

	eventID, err := requiredUUIDHeader(record.Headers, headerEventID)
	if err != nil {
		return audit.CreateAuditEventInput{}, err
	}

	eventVersion, err := requiredIntHeader(record.Headers, headerEventVersion)
	if err != nil {
		return audit.CreateAuditEventInput{}, err
	}

	sourceService, err := requiredHeader(
		record.Headers,
		headerSourceService,
	)
	if err != nil {
		return audit.CreateAuditEventInput{}, err
	}

	aggregateType, err := requiredHeader(
		record.Headers,
		headerAggregateType,
	)
	if err != nil {
		return audit.CreateAuditEventInput{}, err
	}

	aggregateID, err := requiredUUIDHeader(
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

		TraceID: optionalHeader(record.Headers, "trace-id"),

		Details: record.Value,
	}, nil
}

func requiredHeader(
	headers []kafka.RecordHeader,
	key string,
) (string, error) {
	value, ok := kafka.HeaderString(headers, key)
	if !ok || value == "" {
		return "", fmt.Errorf(
			"missing required header %q",
			key,
		)
	}

	return value, nil
}

func requiredUUIDHeader(
	headers []kafka.RecordHeader,
	key string,
) (uuid.UUID, error) {
	value, err := requiredHeader(headers, key)
	if err != nil {
		return uuid.Nil, err
	}

	id, err := uuid.Parse(value)
	if err != nil {
		return uuid.Nil, fmt.Errorf(
			"invalid header %q: %w",
			key,
			err,
		)
	}

	return id, nil
}

func requiredIntHeader(
	headers []kafka.RecordHeader,
	key string,
) (int, error) {
	value, err := requiredHeader(headers, key)
	if err != nil {
		return 0, err
	}

	version, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf(
			"invalid header %q: %w",
			key,
			err,
		)
	}

	return version, nil
}

func optionalHeader(
	headers []kafka.RecordHeader,
	key string,
) *string {
	value, ok := kafka.HeaderString(headers, key)
	if !ok || value == "" {
		return nil
	}

	return &value
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
