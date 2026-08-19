package audit

import (
	"net/netip"

	"github.com/google/uuid"
)

type CreateAuditEventInput struct {
	EventID       uuid.UUID
	EventType     string
	EventVersion  int
	SourceService string

	ActorType *string
	ActorID   *uuid.UUID

	AggregateType string
	AggregateID   uuid.UUID

	TenantID *uuid.UUID
	Status   *string

	IPAddress *netip.Addr
	UserAgent *string

	TraceID *string
	Details []byte
}
