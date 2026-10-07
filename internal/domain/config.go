package domain

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Config drift resolution actions accepted for an unfinished case.
const (
	ConfigActionKeepPinned   = "keep_pinned"
	ConfigActionAdoptCurrent = "adopt_current"
)

// ConfigBaseline is an immutable snapshot of the device configuration
// (firmware version, fixture, and correction parameters) that becomes
// effective at a registered moment in time.
type ConfigBaseline struct {
	ID               string    `json:"id"`
	DeviceID         string    `json:"device_id"`
	FirmwareVersion  string    `json:"firmware_version"`
	Fixture          string    `json:"fixture"`
	CorrectionParams string    `json:"correction_params"`
	RegisteredBy     string    `json:"registered_by"`
	EffectiveAt      time.Time `json:"effective_at"`
	RegisteredAt     time.Time `json:"registered_at"`
}

func NewConfigBaseline(id, deviceID, firmwareVersion, fixture, correctionParams, registeredBy string, effectiveAt, registeredAt time.Time) (ConfigBaseline, error) {
	item := ConfigBaseline{
		ID:               strings.TrimSpace(id),
		DeviceID:         strings.TrimSpace(deviceID),
		FirmwareVersion:  strings.TrimSpace(firmwareVersion),
		Fixture:          strings.TrimSpace(fixture),
		CorrectionParams: strings.TrimSpace(correctionParams),
		RegisteredBy:     strings.TrimSpace(registeredBy),
		EffectiveAt:      effectiveAt.UTC(),
		RegisteredAt:     registeredAt.UTC(),
	}
	if err := item.Validate(); err != nil {
		return ConfigBaseline{}, err
	}
	return item, nil
}

func (b ConfigBaseline) Validate() error {
	if b.ID == "" || b.DeviceID == "" || b.FirmwareVersion == "" || b.Fixture == "" || b.CorrectionParams == "" || b.RegisteredBy == "" {
		return fmt.Errorf("%w: baseline id, device, firmware version, fixture, correction params, and registrar are required", ErrInvalidInput)
	}
	if b.EffectiveAt.IsZero() || b.RegisteredAt.IsZero() {
		return fmt.Errorf("%w: baseline effective and registration times are required", ErrInvalidInput)
	}
	return nil
}

// EffectiveBaseline returns the baseline in force at the given moment: the
// latest registered baseline whose effective time is not after at.
func EffectiveBaseline(items []ConfigBaseline, at time.Time) (ConfigBaseline, bool) {
	var selected ConfigBaseline
	found := false
	for _, item := range items {
		if item.EffectiveAt.After(at) {
			continue
		}
		if !found || item.EffectiveAt.After(selected.EffectiveAt) {
			selected = item
			found = true
		}
	}
	return selected, found
}

// SortBaselines orders baselines deterministically by effective time and id.
func SortBaselines(items []ConfigBaseline) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].EffectiveAt.Equal(items[j].EffectiveAt) {
			return items[i].ID < items[j].ID
		}
		return items[i].EffectiveAt.Before(items[j].EffectiveAt)
	})
}
