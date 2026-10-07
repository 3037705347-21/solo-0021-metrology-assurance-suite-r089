package domain

import "errors"

var (
	ErrInvalidInput             = errors.New("invalid input")
	ErrNotFound                 = errors.New("resource not found")
	ErrDuplicateAssetTag        = errors.New("duplicate asset tag")
	ErrActiveCaseExists         = errors.New("device already has an unfinished case")
	ErrStateConflict            = errors.New("resource state conflict")
	ErrSealAlreadyCreated       = errors.New("case already has an evidence seal")
	ErrConfigBaselineMissing    = errors.New("no effective configuration baseline")
	ErrConfigDrift              = errors.New("case configuration baseline is outdated")
	ErrDuplicateConfigVersion   = errors.New("duplicate configuration version")
	ErrConfigBaselineRegression = errors.New("configuration baseline effective time must move forward")
)

func IsKnownError(err error) bool {
	return errors.Is(err, ErrInvalidInput) ||
		errors.Is(err, ErrNotFound) ||
		errors.Is(err, ErrDuplicateAssetTag) ||
		errors.Is(err, ErrActiveCaseExists) ||
		errors.Is(err, ErrStateConflict) ||
		errors.Is(err, ErrSealAlreadyCreated) ||
		errors.Is(err, ErrConfigBaselineMissing) ||
		errors.Is(err, ErrConfigDrift) ||
		errors.Is(err, ErrDuplicateConfigVersion) ||
		errors.Is(err, ErrConfigBaselineRegression)
}
