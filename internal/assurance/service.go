package assurance

import (
	"context"
	"fmt"
	"strings"
	"time"

	"example.com/solo-0021-metrology-assurance-suite/internal/contracts"
	"example.com/solo-0021-metrology-assurance-suite/internal/domain"
	"example.com/solo-0021-metrology-assurance-suite/internal/repository"
)

type Service struct {
	repository *repository.Repository
	now        func() time.Time
}

func NewService(repo *repository.Repository) *Service {
	return &Service{
		repository: repo,
		now:        time.Now,
	}
}

func (s *Service) RegisterDevice(ctx context.Context, input contracts.RegisterDeviceRequest) (contracts.DeviceResponse, error) {
	var created contracts.DeviceResponse
	tag := domain.NormalizeAssetTag(input.AssetTag)
	err := s.repository.Mutate(ctx, func(m *repository.Mutation) error {
		if _, exists := m.DeviceByTag(tag); exists {
			return fmt.Errorf("%w: %s", domain.ErrDuplicateAssetTag, tag)
		}
		item, err := domain.NewDevice(
			m.NextID("device"),
			input.AssetTag,
			input.Model,
			input.Room,
			input.Steward,
			s.now(),
		)
		if err != nil {
			return err
		}
		if err := m.PutDevice(item); err != nil {
			return err
		}
		event, err := domain.NewAuditEvent(
			m.NextID("event"),
			"device",
			item.ID,
			"",
			"device_enrolled",
			item.Steward,
			"device enrolled for assurance work",
			"",
			s.now(),
		)
		if err != nil {
			return err
		}
		m.AppendEvent(event)
		created = contracts.DeviceResponse{Device: item, ConfigBaselines: []domain.ConfigBaseline{}}
		return nil
	})
	return created, err
}

func (s *Service) Device(ctx context.Context, id string) (contracts.DeviceResponse, error) {
	var result contracts.DeviceResponse
	err := s.repository.Read(ctx, func(snapshot repository.Snapshot) error {
		item, exists := snapshot.Device(strings.TrimSpace(id))
		if !exists {
			return fmt.Errorf("%w: device %s", domain.ErrNotFound, id)
		}
		baselines := snapshot.Baselines(item.ID)
		if baselines == nil {
			baselines = []domain.ConfigBaseline{}
		}
		domain.SortBaselines(baselines)
		result = contracts.DeviceResponse{Device: item, ConfigBaselines: baselines}
		if current, ok := domain.EffectiveBaseline(baselines, s.now()); ok {
			result.CurrentConfigBaseline = &current
		}
		return nil
	})
	return result, err
}

func (s *Service) RegisterConfigBaseline(ctx context.Context, deviceID string, input contracts.RegisterConfigBaselineRequest) (domain.ConfigBaseline, error) {
	var created domain.ConfigBaseline
	err := s.repository.Mutate(ctx, func(m *repository.Mutation) error {
		id := strings.TrimSpace(deviceID)
		device, exists := m.Device(id)
		if !exists {
			return fmt.Errorf("%w: device %s", domain.ErrNotFound, id)
		}
		if !device.IsActive() {
			return fmt.Errorf("%w: device %s is %s", domain.ErrStateConflict, device.ID, device.Status)
		}
		item, err := domain.NewConfigBaseline(
			m.NextID("baseline"),
			device.ID,
			input.FirmwareVersion,
			input.Fixture,
			input.CorrectionParams,
			input.RegisteredBy,
			input.EffectiveAt,
			s.now(),
		)
		if err != nil {
			return err
		}
		if err := m.AppendBaseline(item); err != nil {
			return err
		}
		// Surface the registration on the unfinished case timeline so its
		// event stream shows when the device configuration moved on.
		caseID := ""
		if active, exists := m.ActiveCaseForDevice(device.ID); exists {
			caseID = active.ID
		}
		event, err := domain.NewAuditEvent(
			m.NextID("event"),
			"device",
			device.ID,
			caseID,
			"config_baseline_registered",
			item.RegisteredBy,
			fmt.Sprintf("config baseline %s registered; effective %s", item.ID, item.EffectiveAt.Format(time.RFC3339)),
			item.ID,
			s.now(),
		)
		if err != nil {
			return err
		}
		m.AppendEvent(event)
		created = item
		return nil
	})
	return created, err
}

func (s *Service) ConfigBaselines(ctx context.Context, deviceID string) ([]domain.ConfigBaseline, error) {
	var result []domain.ConfigBaseline
	err := s.repository.Read(ctx, func(snapshot repository.Snapshot) error {
		id := strings.TrimSpace(deviceID)
		if _, exists := snapshot.Device(id); !exists {
			return fmt.Errorf("%w: device %s", domain.ErrNotFound, id)
		}
		result = snapshot.Baselines(id)
		if result == nil {
			result = []domain.ConfigBaseline{}
		}
		domain.SortBaselines(result)
		return nil
	})
	return result, err
}

func (s *Service) OpenCase(ctx context.Context, input contracts.OpenCaseRequest) (contracts.CaseResponse, error) {
	var result contracts.CaseResponse
	err := s.repository.Mutate(ctx, func(m *repository.Mutation) error {
		deviceID := strings.TrimSpace(input.DeviceID)
		device, exists := m.Device(deviceID)
		if !exists {
			return fmt.Errorf("%w: device %s", domain.ErrNotFound, deviceID)
		}
		if !device.IsActive() {
			return fmt.Errorf("%w: device %s is %s", domain.ErrStateConflict, device.ID, device.Status)
		}
		if active, exists := m.ActiveCaseForDevice(device.ID); exists {
			return fmt.Errorf("%w: case %s is still %s", domain.ErrActiveCaseExists, active.ID, active.State)
		}
		baseline, ok := domain.EffectiveBaseline(m.Baselines(device.ID), s.now())
		if !ok {
			return fmt.Errorf("%w: device %s", domain.ErrNoEffectiveBaseline, device.ID)
		}
		item, err := domain.NewVerificationCase(
			m.NextID("case"),
			device.ID,
			input.Nominal,
			input.Tolerance,
			input.Steward,
			baseline.ID,
			s.now(),
		)
		if err != nil {
			return err
		}
		if err := m.PutCase(item); err != nil {
			return err
		}
		event, err := domain.NewAuditEvent(
			m.NextID("event"),
			"case",
			item.ID,
			item.ID,
			"case_opened",
			item.Steward,
			"verification case opened",
			item.ConfigBaselineID,
			s.now(),
		)
		if err != nil {
			return err
		}
		m.AppendEvent(event)
		result = caseResponse(m, item, s.now())
		return nil
	})
	return result, err
}

func (s *Service) Case(ctx context.Context, id string) (contracts.CaseResponse, error) {
	var result contracts.CaseResponse
	err := s.repository.Read(ctx, func(snapshot repository.Snapshot) error {
		item, exists := snapshot.Case(strings.TrimSpace(id))
		if !exists {
			return fmt.Errorf("%w: case %s", domain.ErrNotFound, id)
		}
		result = caseResponseFromSnapshot(snapshot, item, s.now())
		return nil
	})
	return result, err
}

func (s *Service) RecordMeasurement(ctx context.Context, caseID string, input contracts.RecordMeasurementRequest) (contracts.CaseResponse, error) {
	var result contracts.CaseResponse
	err := s.repository.Mutate(ctx, func(m *repository.Mutation) error {
		id := strings.TrimSpace(caseID)
		item, exists := m.Case(id)
		if !exists {
			return fmt.Errorf("%w: case %s", domain.ErrNotFound, id)
		}
		if item.State == domain.CaseAwaitingMeasurement {
			if err := s.requireConfigResolved(m, item); err != nil {
				return err
			}
		}
		measurement, err := domain.NewMeasurement(
			m.NextID("measurement"),
			item.ID,
			input.Observed,
			item.Nominal,
			item.Tolerance,
			input.Technician,
			item.ConfigBaselineID,
			s.now(),
		)
		if err != nil {
			return err
		}
		if err := item.RecordMeasurement(measurement.Accepted, s.now()); err != nil {
			return err
		}
		if err := m.AppendMeasurement(measurement); err != nil {
			return err
		}
		if err := m.UpdateCase(item); err != nil {
			return err
		}
		kind := "measurement_rejected"
		detail := "measurement exceeded tolerance and requires another measurement"
		if measurement.Accepted {
			kind = "measurement_accepted"
			detail = "measurement is within tolerance and ready for review"
		}
		event, err := domain.NewAuditEvent(
			m.NextID("event"),
			"case",
			item.ID,
			item.ID,
			kind,
			measurement.Technician,
			detail,
			item.ConfigBaselineID,
			s.now(),
		)
		if err != nil {
			return err
		}
		m.AppendEvent(event)
		result = caseResponse(m, item, s.now())
		return nil
	})
	return result, err
}

func (s *Service) ReopenCase(ctx context.Context, caseID string, input contracts.ReopenCaseRequest) (contracts.CaseResponse, error) {
	var result contracts.CaseResponse
	err := s.repository.Mutate(ctx, func(m *repository.Mutation) error {
		id := strings.TrimSpace(caseID)
		item, exists := m.Case(id)
		if !exists {
			return fmt.Errorf("%w: case %s", domain.ErrNotFound, id)
		}
		if item.State == domain.CaseRecheckRequired {
			if err := s.requireConfigResolved(m, item); err != nil {
				return err
			}
		}
		if err := item.Reopen(s.now()); err != nil {
			return err
		}
		if err := m.UpdateCase(item); err != nil {
			return err
		}
		actor := strings.TrimSpace(input.Actor)
		reason := strings.TrimSpace(input.Reason)
		if actor == "" || reason == "" {
			return fmt.Errorf("%w: actor and reason are required", domain.ErrInvalidInput)
		}
		event, err := domain.NewAuditEvent(
			m.NextID("event"),
			"case",
			item.ID,
			item.ID,
			"case_reopened",
			actor,
			reason,
			item.ConfigBaselineID,
			s.now(),
		)
		if err != nil {
			return err
		}
		m.AppendEvent(event)
		result = caseResponse(m, item, s.now())
		return nil
	})
	return result, err
}

func (s *Service) ResolveCaseConfig(ctx context.Context, caseID string, input contracts.ResolveCaseConfigRequest) (contracts.CaseResponse, error) {
	var result contracts.CaseResponse
	err := s.repository.Mutate(ctx, func(m *repository.Mutation) error {
		id := strings.TrimSpace(caseID)
		item, exists := m.Case(id)
		if !exists {
			return fmt.Errorf("%w: case %s", domain.ErrNotFound, id)
		}
		actor := strings.TrimSpace(input.Actor)
		action := strings.TrimSpace(input.Action)
		reason := strings.TrimSpace(input.Reason)
		if actor == "" || reason == "" {
			return fmt.Errorf("%w: actor and reason are required", domain.ErrInvalidInput)
		}
		effective, ok := domain.EffectiveBaseline(m.Baselines(item.DeviceID), s.now())
		if !ok {
			return fmt.Errorf("%w: device %s", domain.ErrNoEffectiveBaseline, item.DeviceID)
		}
		var detail string
		switch action {
		case domain.ConfigActionKeepPinned:
			if err := item.AcknowledgeConfig(effective.ID, s.now()); err != nil {
				return err
			}
			detail = fmt.Sprintf("kept pinned config baseline %s despite effective %s: %s", item.ConfigBaselineID, effective.ID, reason)
		case domain.ConfigActionAdoptCurrent:
			previous := item.ConfigBaselineID
			if err := item.AdoptConfig(effective.ID, s.now()); err != nil {
				return err
			}
			detail = fmt.Sprintf("adopted config baseline %s (was %s): %s", effective.ID, previous, reason)
		default:
			return fmt.Errorf("%w: action must be keep_pinned or adopt_current", domain.ErrInvalidInput)
		}
		if err := m.UpdateCase(item); err != nil {
			return err
		}
		event, err := domain.NewAuditEvent(
			m.NextID("event"),
			"case",
			item.ID,
			item.ID,
			"case_config_resolved",
			actor,
			detail,
			item.ConfigBaselineID,
			s.now(),
		)
		if err != nil {
			return err
		}
		m.AppendEvent(event)
		result = caseResponse(m, item, s.now())
		return nil
	})
	return result, err
}

func (s *Service) SealCase(ctx context.Context, caseID string, input contracts.SealCaseRequest) (contracts.CaseResponse, error) {
	var result contracts.CaseResponse
	err := s.repository.Mutate(ctx, func(m *repository.Mutation) error {
		id := strings.TrimSpace(caseID)
		item, exists := m.Case(id)
		if !exists {
			return fmt.Errorf("%w: case %s", domain.ErrNotFound, id)
		}
		if _, exists := m.Seal(item.ID); exists {
			return fmt.Errorf("%w: case %s", domain.ErrSealAlreadyCreated, item.ID)
		}
		if item.State == domain.CaseReviewReady {
			if err := s.requireConfigResolved(m, item); err != nil {
				return err
			}
		}
		measurement, ok := bestAcceptedMeasurement(m.Measurements(item.ID))
		if !ok {
			return fmt.Errorf("%w: an accepted measurement is required before sealing", domain.ErrStateConflict)
		}
		if err := item.Seal(s.now()); err != nil {
			return err
		}
		seal, err := domain.NewEvidenceSeal(
			m.NextID("seal"),
			item.ID,
			item.DeviceID,
			measurement.ID,
			input.Reviewer,
			input.Summary,
			item.ConfigBaselineID,
			s.now(),
		)
		if err != nil {
			return err
		}
		if err := m.PutSeal(seal); err != nil {
			return err
		}
		if err := m.UpdateCase(item); err != nil {
			return err
		}
		event, err := domain.NewAuditEvent(
			m.NextID("event"),
			"case",
			item.ID,
			item.ID,
			"case_sealed",
			seal.Reviewer,
			seal.Summary,
			item.ConfigBaselineID,
			s.now(),
		)
		if err != nil {
			return err
		}
		m.AppendEvent(event)
		result = caseResponse(m, item, s.now())
		return nil
	})
	return result, err
}

// requireConfigResolved blocks case progress while the baseline effective for
// the device has moved past the configuration the case is allowed to use.
func (s *Service) requireConfigResolved(m *repository.Mutation, item domain.VerificationCase) error {
	effective, ok := domain.EffectiveBaseline(m.Baselines(item.DeviceID), s.now())
	if !ok {
		return nil
	}
	if item.ConfigDrifted(effective.ID) {
		return fmt.Errorf("%w: case %s pins config baseline %s but %s is now effective", domain.ErrUnresolvedConfigDrift, item.ID, item.ConfigBaselineID, effective.ID)
	}
	return nil
}

func bestAcceptedMeasurement(items []domain.Measurement) (domain.Measurement, bool) {
	var selected domain.Measurement
	found := false
	for _, item := range items {
		if !item.Accepted {
			continue
		}
		if !found || item.AbsoluteDeviation() < selected.AbsoluteDeviation() {
			selected = item
			found = true
		}
	}
	return selected, found
}

func caseResponse(m *repository.Mutation, item domain.VerificationCase, now time.Time) contracts.CaseResponse {
	var seal *domain.EvidenceSeal
	if found, exists := m.Seal(item.ID); exists {
		seal = &found
	}
	return buildCaseResponse(item, m.Measurements(item.ID), m.Events(item.ID), seal, m.Baselines(item.DeviceID), now)
}

func caseResponseFromSnapshot(snapshot repository.Snapshot, item domain.VerificationCase, now time.Time) contracts.CaseResponse {
	var seal *domain.EvidenceSeal
	if found, exists := snapshot.Seal(item.ID); exists {
		seal = &found
	}
	return buildCaseResponse(item, snapshot.Measurements(item.ID), snapshot.Events(item.ID), seal, snapshot.Baselines(item.DeviceID), now)
}

// buildCaseResponse derives the configuration status and embeds every
// baseline the case, its measurements, its seal, and its events decided
// with, so a read can reconstruct the configuration source of each decision.
func buildCaseResponse(item domain.VerificationCase, measurements []domain.Measurement, events []domain.AuditEvent, seal *domain.EvidenceSeal, baselines []domain.ConfigBaseline, now time.Time) contracts.CaseResponse {
	status := "current"
	effective, hasEffective := domain.EffectiveBaseline(baselines, now)
	if hasEffective && item.ConfigDrifted(effective.ID) {
		status = "superseded"
	}
	referenced := map[string]bool{item.ConfigBaselineID: true}
	if item.AcknowledgedConfigID != "" {
		referenced[item.AcknowledgedConfigID] = true
	}
	if hasEffective {
		referenced[effective.ID] = true
	}
	for _, measurement := range measurements {
		referenced[measurement.ConfigBaselineID] = true
	}
	if seal != nil {
		referenced[seal.ConfigBaselineID] = true
	}
	for _, event := range events {
		if event.ConfigBaselineID != "" {
			referenced[event.ConfigBaselineID] = true
		}
	}
	included := make([]domain.ConfigBaseline, 0, len(referenced))
	for _, baseline := range baselines {
		if referenced[baseline.ID] {
			included = append(included, baseline)
		}
	}
	domain.SortBaselines(included)
	return contracts.CaseResponse{
		Case:            item,
		Measurements:    measurements,
		Seal:            seal,
		Events:          events,
		ConfigStatus:    status,
		ConfigBaselines: included,
	}
}
