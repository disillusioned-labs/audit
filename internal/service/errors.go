// Package service holds cross-resource service bits: the domain error type
// and storage-error helpers. Each resource's business logic lives in its own
// subpackage (service/user, ...), exposing an interface plus an unexported
// implementation so handlers can mock the contract in tests.
//
// Domain errors are self-describing: each carries the HTTP status and
// machine-readable code it maps to. handler.WriteServiceError reads those
// fields, so adding a resource never means editing the shared handler layer.
package service

import (
	"github.com/disillusioned-labs/platform/errors"
)

// Error is a domain error that knows how it surfaces over HTTP.
type Error = errors.Error

// NewError builds a domain error for a resource-specific failure.
var NewError = errors.NewError

var (
	ErrUnauthenticated = errors.ErrUnauthenticated
	ErrForbidden       = errors.ErrForbidden
	ErrNotFound        = errors.ErrNotFound
	ErrConflict        = errors.ErrConflict
	ErrInternal        = errors.ErrInternal
)

// IsUniqueViolation reports whether err is a Postgres unique-constraint violation.
var IsUniqueViolation = errors.IsUniqueViolation
