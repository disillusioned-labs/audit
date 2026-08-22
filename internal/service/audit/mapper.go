package audit

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"strconv"

	"github.com/disillusioned-labs/audit/internal/platform/kafka"
	"github.com/google/uuid"
	"github.com/twmb/franz-go/pkg/kgo"
)

func MapKafkaRecordToCreateAuditEventInput(
	record *kgo.Record,
) (CreateAuditEventInput, error) {
	if record == nil {
		return CreateAuditEventInput{}, fmt.Errorf(
			"kafka record must not be nil",
		)
	}

	eventID, err := requiredUUIDHeader(record, "event-id")
	if err != nil {
		return CreateAuditEventInput{}, err
	}

	eventVersion, err := requiredIntHeader(record, "event-version")
	if err != nil {
		return CreateAuditEventInput{}, err
	}

	sourceService, err := requiredStringHeader(
		record,
		"source-service",
	)
	if err != nil {
		return CreateAuditEventInput{}, err
	}

	aggregateType, err := requiredStringHeader(
		record,
		"aggregate-type",
	)
	if err != nil {
		return CreateAuditEventInput{}, err
	}

	aggregateID, err := requiredUUIDHeader(
		record,
		"aggregate-id",
	)
	if err != nil {
		return CreateAuditEventInput{}, err
	}

	var recordData map[string]string
	err = json.Unmarshal(record.Value, &recordData)
	if err != nil {
		return CreateAuditEventInput{}, err
	}

	return CreateAuditEventInput{
		EventID:       eventID,
		EventType:     record.Topic,
		EventVersion:  eventVersion,
		SourceService: sourceService,
		AggregateType: aggregateType,
		AggregateID:   aggregateID,

		IPAddress: parseIP(recordData["ip_address"]),
		UserAgent: optionalString(recordData["user_agent"]),

		// Optional event metadata.
		TraceID: optionalStringHeader(record, "trace-id"),
		// Event-specific payload.
		Details: record.Value,
	}, nil
}

func requiredStringHeader(
	record *kgo.Record,
	key string,
) (string, error) {
	value, ok := kafka.HeaderString(record, key)
	if !ok || value == "" {
		return "", fmt.Errorf(
			"missing required kafka header %q",
			key,
		)
	}

	return value, nil
}

func requiredUUIDHeader(
	record *kgo.Record,
	key string,
) (uuid.UUID, error) {
	value, err := requiredStringHeader(record, key)
	if err != nil {
		return uuid.Nil, err
	}

	id, err := uuid.Parse(value)
	if err != nil {
		return uuid.Nil, fmt.Errorf(
			"invalid kafka header %q: %w",
			key,
			err,
		)
	}

	return id, nil
}

func requiredIntHeader(
	record *kgo.Record,
	key string,
) (int, error) {
	value, err := requiredStringHeader(record, key)
	if err != nil {
		return 0, err
	}

	version, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf(
			"invalid kafka header %q: %w",
			key,
			err,
		)
	}

	return version, nil
}

func optionalStringHeader(
	record *kgo.Record,
	key string,
) *string {
	value, ok := kafka.HeaderString(record, key)
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
