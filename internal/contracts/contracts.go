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
	FirmwareVersion  string    `json:"firmware_version"`
	Fixture          string    `json:"fixture"`
	CorrectionParams string    `json:"correction_params"`
	EffectiveAt      time.Time `json:"effective_at"`
	RegisteredBy     string    `json:"registered_by"`
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

type ResolveCaseConfigRequest struct {
	Actor  string `json:"actor"`
	Action string `json:"action"`
	Reason string `json:"reason"`
}

type SealCaseRequest struct {
	Reviewer string `json:"reviewer"`
	Summary  string `json:"summary"`
}

type DeviceResponse struct {
	Device                domain.Device           `json:"device"`
	ConfigBaselines       []domain.ConfigBaseline `json:"config_baselines"`
	CurrentConfigBaseline *domain.ConfigBaseline  `json:"current_config_baseline,omitempty"`
}

type ConfigBaselineResponse struct {
	ConfigBaseline domain.ConfigBaseline `json:"config_baseline"`
}

type ConfigBaselinesResponse struct {
	ConfigBaselines []domain.ConfigBaseline `json:"config_baselines"`
}

type CaseResponse struct {
	Case            domain.VerificationCase `json:"case"`
	Measurements    []domain.Measurement    `json:"measurements"`
	Seal            *domain.EvidenceSeal    `json:"seal,omitempty"`
	Events          []domain.AuditEvent     `json:"events"`
	ConfigStatus    string                  `json:"config_status"`
	ConfigBaselines []domain.ConfigBaseline `json:"config_baselines"`
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
