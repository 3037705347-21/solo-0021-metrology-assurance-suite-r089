package domain

import (
	"fmt"
	"math"
	"strings"
	"time"
)

type CaseState string

const (
	CaseAwaitingMeasurement CaseState = "awaiting_measurement"
	CaseReviewReady         CaseState = "review_ready"
	CaseRecheckRequired     CaseState = "recheck_required"
	CaseSealed              CaseState = "sealed"
)

type VerificationCase struct {
	ID                   string     `json:"id"`
	DeviceID             string     `json:"device_id"`
	Nominal              float64    `json:"nominal"`
	Tolerance            float64    `json:"tolerance"`
	Steward              string     `json:"steward"`
	State                CaseState  `json:"state"`
	Version              int        `json:"version"`
	ConfigBaselineID     string     `json:"config_baseline_id"`
	AcknowledgedConfigID string     `json:"acknowledged_config_id,omitempty"`
	OpenedAt             time.Time  `json:"opened_at"`
	UpdatedAt            time.Time  `json:"updated_at"`
	SealedAt             *time.Time `json:"sealed_at,omitempty"`
}

func NewVerificationCase(id, deviceID string, nominal, tolerance float64, steward, configBaselineID string, at time.Time) (VerificationCase, error) {
	item := VerificationCase{
		ID:               strings.TrimSpace(id),
		DeviceID:         strings.TrimSpace(deviceID),
		Nominal:          nominal,
		Tolerance:        tolerance,
		Steward:          strings.TrimSpace(steward),
		State:            CaseAwaitingMeasurement,
		Version:          1,
		ConfigBaselineID: strings.TrimSpace(configBaselineID),
		OpenedAt:         at.UTC(),
		UpdatedAt:        at.UTC(),
	}
	if err := item.Validate(); err != nil {
		return VerificationCase{}, err
	}
	return item, nil
}

func (c VerificationCase) Validate() error {
	if c.ID == "" || c.DeviceID == "" || c.Steward == "" || c.ConfigBaselineID == "" {
		return fmt.Errorf("%w: case id, device id, steward, and config baseline are required", ErrInvalidInput)
	}
	if !isFinite(c.Nominal) || !isFinite(c.Tolerance) || c.Tolerance <= 0 {
		return fmt.Errorf("%w: nominal must be finite and tolerance must be positive", ErrInvalidInput)
	}
	if c.Version < 1 || c.OpenedAt.IsZero() || c.UpdatedAt.IsZero() {
		return fmt.Errorf("%w: case version and timestamps are required", ErrInvalidInput)
	}
	switch c.State {
	case CaseAwaitingMeasurement, CaseReviewReady, CaseRecheckRequired, CaseSealed:
		return nil
	default:
		return fmt.Errorf("%w: unsupported case state %q", ErrInvalidInput, c.State)
	}
}

func (c VerificationCase) IsUnfinished() bool {
	return c.State != CaseSealed
}

func (c *VerificationCase) RecordMeasurement(accepted bool, at time.Time) error {
	if c.State != CaseAwaitingMeasurement {
		return fmt.Errorf("%w: case is not awaiting measurement", ErrStateConflict)
	}
	if accepted {
		c.State = CaseReviewReady
	} else {
		c.State = CaseRecheckRequired
	}
	c.Version++
	c.UpdatedAt = at.UTC()
	return nil
}

func (c *VerificationCase) Reopen(at time.Time) error {
	if c.State != CaseRecheckRequired {
		return fmt.Errorf("%w: only a recheck_required case can be reopened", ErrStateConflict)
	}
	c.State = CaseAwaitingMeasurement
	c.Version++
	c.UpdatedAt = at.UTC()
	return nil
}

func (c *VerificationCase) Seal(at time.Time) error {
	if c.State != CaseReviewReady {
		return fmt.Errorf("%w: case is not ready for review", ErrStateConflict)
	}
	stamp := at.UTC()
	c.State = CaseSealed
	c.Version++
	c.UpdatedAt = stamp
	c.SealedAt = &stamp
	return nil
}

// ConfigDrifted reports whether the baseline now effective for the device
// differs from the configuration the case is allowed to decide with.
func (c VerificationCase) ConfigDrifted(effectiveBaselineID string) bool {
	return effectiveBaselineID != "" &&
		effectiveBaselineID != c.ConfigBaselineID &&
		effectiveBaselineID != c.AcknowledgedConfigID
}

// AcknowledgeConfig keeps the pinned baseline while recording that the
// steward explicitly accepted the divergent effective baseline.
func (c *VerificationCase) AcknowledgeConfig(effectiveBaselineID string, at time.Time) error {
	if c.State == CaseSealed {
		return fmt.Errorf("%w: a sealed case cannot change configuration", ErrStateConflict)
	}
	if !c.ConfigDrifted(effectiveBaselineID) {
		return fmt.Errorf("%w: case has no unresolved config drift", ErrStateConflict)
	}
	c.AcknowledgedConfigID = effectiveBaselineID
	c.Version++
	c.UpdatedAt = at.UTC()
	return nil
}

// AdoptConfig re-pins the case to the baseline now effective for the device.
func (c *VerificationCase) AdoptConfig(effectiveBaselineID string, at time.Time) error {
	if c.State == CaseSealed {
		return fmt.Errorf("%w: a sealed case cannot change configuration", ErrStateConflict)
	}
	if !c.ConfigDrifted(effectiveBaselineID) {
		return fmt.Errorf("%w: case has no unresolved config drift", ErrStateConflict)
	}
	c.ConfigBaselineID = effectiveBaselineID
	c.AcknowledgedConfigID = ""
	c.Version++
	c.UpdatedAt = at.UTC()
	return nil
}

func isFinite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}
