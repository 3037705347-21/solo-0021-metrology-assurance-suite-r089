package contracts

import (
	"time"

	"example.com/solo-0021-metrology-assurance-suite/internal/domain"
)

type RegisterDeviceRequest struct {
	AssetTag string `json:"asset_tag"`
	Model    string `json:"model"`
	Room     string `json:"room"`
	Steward  string `json:"steward"`
}

type RegisterConfigBaselineRequest struct {
	Version     string    `json:"version"`
	Note        string    `json:"note"`
	EffectiveAt time.Time `json:"effective_at"`
	Steward     string    `json:"steward"`
}

type OpenCaseRequest struct {
	DeviceID  string  `json:"device_id"`
	Nominal   float64 `json:"nominal"`
	Tolerance float64 `json:"tolerance"`
	Steward   string  `json:"steward"`
}

type RecordMeasurementRequest struct {
	Observed   float64 `json:"observed"`
	Technician string  `json:"technician"`
}

type ReopenCaseRequest struct {
	Actor  string `json:"actor"`
	Reason string `json:"reason"`
}

type RebaselineCaseRequest struct {
	Actor  string `json:"actor"`
	Reason string `json:"reason"`
}

type SealCaseRequest struct {
	Reviewer string `json:"reviewer"`
	Summary  string `json:"summary"`
}

type DeviceResponse struct {
	Device domain.Device `json:"device"`
}

type ConfigBaselineResponse struct {
	Baseline domain.ConfigBaseline `json:"baseline"`
}

type ConfigBaselinesResponse struct {
	Baselines []domain.ConfigBaseline `json:"baselines"`
}

type CaseResponse struct {
	Case            domain.VerificationCase `json:"case"`
	Baseline        *domain.ConfigBaseline  `json:"baseline,omitempty"`
	CurrentBaseline *domain.ConfigBaseline  `json:"current_baseline,omitempty"`
	ConfigDrifted   bool                    `json:"config_drifted"`
	Measurements    []domain.Measurement    `json:"measurements"`
	Seal            *domain.EvidenceSeal    `json:"seal,omitempty"`
	Events          []domain.AuditEvent     `json:"events"`
}

type EventsResponse struct {
	Events []domain.AuditEvent `json:"events"`
}

type ErrorResponse struct {
	Error ErrorBody `json:"error"`
}

type ErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
