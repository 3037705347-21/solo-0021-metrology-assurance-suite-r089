package domain

import (
	"fmt"
	"strings"
	"time"
)

type ConfigBaseline struct {
	ID           string    `json:"id"`
	DeviceID     string    `json:"device_id"`
	Version      string    `json:"version"`
	Note         string    `json:"note,omitempty"`
	EffectiveAt  time.Time `json:"effective_at"`
	RegisteredBy string    `json:"registered_by"`
	RegisteredAt time.Time `json:"registered_at"`
}

func NewConfigBaseline(id, deviceID, version, note, registeredBy string, effectiveAt, registeredAt time.Time) (ConfigBaseline, error) {
	item := ConfigBaseline{
		ID:           strings.TrimSpace(id),
		DeviceID:     strings.TrimSpace(deviceID),
		Version:      strings.TrimSpace(version),
		Note:         strings.TrimSpace(note),
		EffectiveAt:  effectiveAt.UTC(),
		RegisteredBy: strings.TrimSpace(registeredBy),
		RegisteredAt: registeredAt.UTC(),
	}
	if err := item.Validate(); err != nil {
		return ConfigBaseline{}, err
	}
	return item, nil
}

func (b ConfigBaseline) Validate() error {
	if b.ID == "" || b.DeviceID == "" || b.Version == "" || b.RegisteredBy == "" {
		return fmt.Errorf("%w: baseline id, device id, version, and steward are required", ErrInvalidInput)
	}
	if len(b.Version) > 128 {
		return fmt.Errorf("%w: configuration version is too long", ErrInvalidInput)
	}
	if len(b.Note) > 256 {
		return fmt.Errorf("%w: configuration note is too long", ErrInvalidInput)
	}
	if b.EffectiveAt.IsZero() || b.RegisteredAt.IsZero() {
		return fmt.Errorf("%w: baseline effective and registration times are required", ErrInvalidInput)
	}
	return nil
}

func CurrentBaseline(items []ConfigBaseline, at time.Time) (ConfigBaseline, bool) {
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
