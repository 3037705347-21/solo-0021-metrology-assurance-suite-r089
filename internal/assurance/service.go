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

func (s *Service) RegisterDevice(ctx context.Context, input contracts.RegisterDeviceRequest) (domain.Device, error) {
	var created domain.Device
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
			"",
			"device_enrolled",
			item.Steward,
			"device enrolled for assurance work",
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

func (s *Service) Device(ctx context.Context, id string) (domain.Device, error) {
	var found domain.Device
	err := s.repository.Read(ctx, func(snapshot repository.Snapshot) error {
		item, exists := snapshot.Device(strings.TrimSpace(id))
		if !exists {
			return fmt.Errorf("%w: device %s", domain.ErrNotFound, id)
		}
		found = item
		return nil
	})
	return found, err
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
		version := strings.TrimSpace(input.Version)
		if _, exists := m.BaselineByVersion(device.ID, version); exists {
			return fmt.Errorf("%w: %s", domain.ErrDuplicateConfigVersion, version)
		}
		item, err := domain.NewConfigBaseline(
			m.NextID("baseline"),
			device.ID,
			version,
			input.Note,
			input.Steward,
			input.EffectiveAt,
			s.now(),
		)
		if err != nil {
			return err
		}
		if baselines := m.Baselines(device.ID); len(baselines) > 0 {
			latest := baselines[len(baselines)-1]
			if !item.EffectiveAt.After(latest.EffectiveAt) {
				return fmt.Errorf("%w: effective time must be later than %s", domain.ErrConfigBaselineRegression, latest.EffectiveAt.Format(time.RFC3339))
			}
		}
		if err := m.PutBaseline(item); err != nil {
			return err
		}
		event, err := domain.NewAuditEvent(
			m.NextID("event"),
			"device",
			device.ID,
			"",
			item.ID,
			"device_config_registered",
			item.RegisteredBy,
			fmt.Sprintf("configuration baseline %s registered; effective %s", item.Version, item.EffectiveAt.Format(time.RFC3339)),
			s.now(),
		)
		if err != nil {
			return err
		}
		m.AppendEvent(event)
		if active, exists := m.ActiveCaseForDevice(device.ID); exists && !item.EffectiveAt.After(s.now()) && active.BaselineID != item.ID {
			notice, err := domain.NewAuditEvent(
				m.NextID("event"),
				"case",
				active.ID,
				active.ID,
				item.ID,
				"config_drift_pending",
				item.RegisteredBy,
				fmt.Sprintf("configuration changed from %s to %s; rebaseline is required before further measurements or sealing", baselineLabel(m, active.BaselineID), baselineLabel(m, item.ID)),
				s.now(),
			)
			if err != nil {
				return err
			}
			m.AppendEvent(notice)
		}
		created = item
		return nil
	})
	return created, err
}

func (s *Service) ConfigBaselines(ctx context.Context, deviceID string) (contracts.ConfigBaselinesResponse, error) {
	var result contracts.ConfigBaselinesResponse
	err := s.repository.Read(ctx, func(snapshot repository.Snapshot) error {
		id := strings.TrimSpace(deviceID)
		if _, exists := snapshot.Device(id); !exists {
			return fmt.Errorf("%w: device %s", domain.ErrNotFound, id)
		}
		result = contracts.ConfigBaselinesResponse{Baselines: snapshot.Baselines(id)}
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
		baseline, exists := domain.CurrentBaseline(m.Baselines(device.ID), s.now())
		if !exists {
			return fmt.Errorf("%w: device %s has no effective configuration baseline", domain.ErrConfigBaselineMissing, device.ID)
		}
		item, err := domain.NewVerificationCase(
			m.NextID("case"),
			device.ID,
			baseline.ID,
			input.Nominal,
			input.Tolerance,
			input.Steward,
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
			baseline.ID,
			"case_opened",
			item.Steward,
			fmt.Sprintf("verification case opened under configuration %s", baseline.Version),
			s.now(),
		)
		if err != nil {
			return err
		}
		m.AppendEvent(event)
		result = s.caseResponse(m, item)
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
		result = s.caseResponseFromSnapshot(snapshot, item)
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
			if err := configDriftError(m, item, s.now()); err != nil {
				return err
			}
		}
		measurement, err := domain.NewMeasurement(
			m.NextID("measurement"),
			item.ID,
			item.BaselineID,
			input.Observed,
			item.Nominal,
			item.Tolerance,
			input.Technician,
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
			item.BaselineID,
			kind,
			measurement.Technician,
			detail,
			s.now(),
		)
		if err != nil {
			return err
		}
		m.AppendEvent(event)
		result = s.caseResponse(m, item)
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
			item.BaselineID,
			"case_reopened",
			actor,
			reason,
			s.now(),
		)
		if err != nil {
			return err
		}
		m.AppendEvent(event)
		result = s.caseResponse(m, item)
		return nil
	})
	return result, err
}

func (s *Service) RebaselineCase(ctx context.Context, caseID string, input contracts.RebaselineCaseRequest) (contracts.CaseResponse, error) {
	var result contracts.CaseResponse
	err := s.repository.Mutate(ctx, func(m *repository.Mutation) error {
		id := strings.TrimSpace(caseID)
		item, exists := m.Case(id)
		if !exists {
			return fmt.Errorf("%w: case %s", domain.ErrNotFound, id)
		}
		actor := strings.TrimSpace(input.Actor)
		reason := strings.TrimSpace(input.Reason)
		if actor == "" || reason == "" {
			return fmt.Errorf("%w: actor and reason are required", domain.ErrInvalidInput)
		}
		current, exists := domain.CurrentBaseline(m.Baselines(item.DeviceID), s.now())
		if !exists {
			return fmt.Errorf("%w: device %s has no effective configuration baseline", domain.ErrConfigBaselineMissing, item.DeviceID)
		}
		previousLabel := baselineLabel(m, item.BaselineID)
		if err := item.Rebaseline(current.ID, s.now()); err != nil {
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
			current.ID,
			"case_rebaselined",
			actor,
			fmt.Sprintf("%s; configuration baseline changed from %s to %s", reason, previousLabel, baselineLabel(m, current.ID)),
			s.now(),
		)
		if err != nil {
			return err
		}
		m.AppendEvent(event)
		result = s.caseResponse(m, item)
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
		measurement, ok := bestAcceptedMeasurement(m.Measurements(item.ID))
		if !ok {
			return fmt.Errorf("%w: an accepted measurement is required before sealing", domain.ErrStateConflict)
		}
		if item.State == domain.CaseReviewReady {
			if err := configDriftError(m, item, s.now()); err != nil {
				return err
			}
		}
		if err := item.Seal(s.now()); err != nil {
			return err
		}
		seal, err := domain.NewEvidenceSeal(
			m.NextID("seal"),
			item.ID,
			item.DeviceID,
			measurement.ID,
			item.BaselineID,
			input.Reviewer,
			input.Summary,
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
			item.BaselineID,
			"case_sealed",
			seal.Reviewer,
			seal.Summary,
			s.now(),
		)
		if err != nil {
			return err
		}
		m.AppendEvent(event)
		result = s.caseResponse(m, item)
		return nil
	})
	return result, err
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

func configDriftError(m *repository.Mutation, item domain.VerificationCase, at time.Time) error {
	current, exists := domain.CurrentBaseline(m.Baselines(item.DeviceID), at)
	if !exists {
		return fmt.Errorf("%w: device %s has no effective configuration baseline", domain.ErrConfigBaselineMissing, item.DeviceID)
	}
	if current.ID != item.BaselineID {
		return fmt.Errorf("%w: case %s is pinned to configuration %s but %s is now in effect", domain.ErrConfigDrift, item.ID, item.BaselineID, current.ID)
	}
	return nil
}

func baselineLabel(m *repository.Mutation, baselineID string) string {
	if item, exists := m.Baseline(baselineID); exists && item.Version != "" {
		return item.Version
	}
	return baselineID
}

type baselineSource interface {
	Baseline(id string) (domain.ConfigBaseline, bool)
	Baselines(deviceID string) []domain.ConfigBaseline
}

func (s *Service) decorateCase(source baselineSource, item domain.VerificationCase, result *contracts.CaseResponse) {
	if baseline, exists := source.Baseline(item.BaselineID); exists {
		result.Baseline = &baseline
	}
	if current, exists := domain.CurrentBaseline(source.Baselines(item.DeviceID), s.now()); exists {
		result.CurrentBaseline = &current
		result.ConfigDrifted = item.IsUnfinished() && current.ID != item.BaselineID
	}
}

func (s *Service) caseResponse(m *repository.Mutation, item domain.VerificationCase) contracts.CaseResponse {
	result := contracts.CaseResponse{
		Case:         item,
		Measurements: m.Measurements(item.ID),
		Events:       m.Events(item.ID),
	}
	if seal, exists := m.Seal(item.ID); exists {
		result.Seal = &seal
	}
	s.decorateCase(m, item, &result)
	return result
}

func (s *Service) caseResponseFromSnapshot(snapshot repository.Snapshot, item domain.VerificationCase) contracts.CaseResponse {
	result := contracts.CaseResponse{
		Case:         item,
		Measurements: snapshot.Measurements(item.ID),
		Events:       snapshot.Events(item.ID),
	}
	if seal, exists := snapshot.Seal(item.ID); exists {
		result.Seal = &seal
	}
	s.decorateCase(snapshot, item, &result)
	return result
}
