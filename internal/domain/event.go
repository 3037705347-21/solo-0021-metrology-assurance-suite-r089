package domain

import (
	"fmt"
	"strings"
	"time"
)

type AuditEvent struct {
	ID               string    `json:"id"`
	SubjectType      string    `json:"subject_type"`
	SubjectID        string    `json:"subject_id"`
	CaseID           string    `json:"case_id,omitempty"`
	Kind             string    `json:"kind"`
	Actor            string    `json:"actor"`
	Detail           string    `json:"detail"`
	ConfigBaselineID string    `json:"config_baseline_id,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
}

func NewAuditEvent(id, subjectType, subjectID, caseID, kind, actor, detail, configBaselineID string, at time.Time) (AuditEvent, error) {
	item := AuditEvent{
		ID:               strings.TrimSpace(id),
		SubjectType:      strings.TrimSpace(subjectType),
		SubjectID:        strings.TrimSpace(subjectID),
		CaseID:           strings.TrimSpace(caseID),
		Kind:             strings.TrimSpace(kind),
		Actor:            strings.TrimSpace(actor),
		Detail:           strings.TrimSpace(detail),
		ConfigBaselineID: strings.TrimSpace(configBaselineID),
		CreatedAt:        at.UTC(),
	}
	if err := item.Validate(); err != nil {
		return AuditEvent{}, err
	}
	return item, nil
}

func (e AuditEvent) Validate() error {
	if e.ID == "" || e.SubjectType == "" || e.SubjectID == "" || e.Kind == "" || e.Actor == "" || e.Detail == "" {
		return fmt.Errorf("%w: audit event fields are required", ErrInvalidInput)
	}
	if e.CreatedAt.IsZero() {
		return fmt.Errorf("%w: event time is required", ErrInvalidInput)
	}
	return nil
}
