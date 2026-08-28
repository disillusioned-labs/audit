package consumer

import "errors"

var (
	ErrPermanent = errors.New(
		"permanent kafka processing error",
	)

	ErrTransient = errors.New(
		"transient kafka processing error",
	)
)

func Permanent(err error) error {
	if err == nil {
		return nil
	}

	return errors.Join(
		ErrPermanent,
		err,
	)
}

func Transient(err error) error {
	if err == nil {
		return nil
	}

	return errors.Join(
		ErrTransient,
		err,
	)
}

func IsPermanent(err error) bool {
	return errors.Is(err, ErrPermanent)
}

func IsTransient(err error) bool {
	return errors.Is(err, ErrTransient)
}

func classifyError(err error) errorType {
	switch {
	case IsPermanent(err):
		return errorTypePermanent

	case IsTransient(err):
		return errorTypeTransient

	default:
		return errorTypeUnknown
	}
}
