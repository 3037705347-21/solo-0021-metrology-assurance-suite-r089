package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"time"

	"example.com/solo-0021-metrology-assurance-suite/internal/bootstrap"
)

type apiResponse struct {
	Status int
	Body   map[string]any
	Raw    string
}

type harness struct {
	baseURL string
	client  *http.Client
	server  *httptest.Server
}

func main() {
	workflow := flag.String("workflow", "", "workflow id to verify")
	flag.Parse()

	checks := map[string]func() error{
		"register-device":          checkRegisterDevice,
		"open-verification-case":   checkOpenVerificationCase,
		"record-measurement":       checkRecordMeasurement,
		"reopen-verification-case": checkReopenVerificationCase,
		"seal-verification":        checkSealVerification,
		"config-baseline":          checkConfigBaseline,
	}
	run, ok := checks[*workflow]
	if !ok {
		fmt.Fprintf(flag.CommandLine.Output(), "unknown workflow %q\n", *workflow)
		flag.Usage()
		os.Exit(2)
	}
	if err := run(); err != nil {
		fmt.Printf("workflow %s: failed: %v\n", *workflow, err)
		os.Exit(1)
	}
	fmt.Printf("workflow %s: ok\n", *workflow)
}

func checkRegisterDevice() error {
	h := newHarness()
	defer h.close()

	response, err := h.request(http.MethodPost, "/devices", map[string]any{
		"asset_tag": " met-1001 ",
		"model":     "Bench Meter",
		"room":      "Room A",
		"steward":   "Lin",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(response, http.StatusCreated); err != nil {
		return err
	}
	id, err := nestedString(response.Body, "device", "id")
	if err != nil {
		return err
	}
	tag, err := nestedString(response.Body, "device", "asset_tag")
	if err != nil {
		return err
	}
	if tag != "MET-1001" {
		return fmt.Errorf("asset tag was not normalized: %q", tag)
	}

	read, err := h.request(http.MethodGet, "/devices/"+id, nil)
	if err != nil {
		return err
	}
	if err := expectStatus(read, http.StatusOK); err != nil {
		return err
	}
	readID, err := nestedString(read.Body, "device", "id")
	if err != nil {
		return err
	}
	if readID != id {
		return fmt.Errorf("read returned device %q, expected %q", readID, id)
	}

	duplicate, err := h.request(http.MethodPost, "/devices", map[string]any{
		"asset_tag": "MET-1001",
		"model":     "Other Meter",
		"room":      "Room B",
		"steward":   "Mira",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(duplicate, http.StatusConflict); err != nil {
		return err
	}
	return expectErrorCode(duplicate, "duplicate_asset_tag")
}

func checkOpenVerificationCase() error {
	h := newHarness()
	defer h.close()

	deviceID, err := h.deviceWithBaseline("MET-1101")
	if err != nil {
		return err
	}
	opened, err := h.openCase(deviceID)
	if err != nil {
		return err
	}
	caseID, err := nestedString(opened.Body, "case", "id")
	if err != nil {
		return err
	}
	state, err := nestedString(opened.Body, "case", "state")
	if err != nil {
		return err
	}
	if state != "awaiting_measurement" {
		return fmt.Errorf("opened case state = %q", state)
	}

	read, err := h.request(http.MethodGet, "/cases/"+caseID, nil)
	if err != nil {
		return err
	}
	if err := expectStatus(read, http.StatusOK); err != nil {
		return err
	}

	second, err := h.request(http.MethodPost, "/cases", map[string]any{
		"device_id": deviceID,
		"nominal":   25.0,
		"tolerance": 0.5,
		"steward":   "Qiao",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(second, http.StatusConflict); err != nil {
		return err
	}
	return expectErrorCode(second, "active_case_exists")
}

func checkRecordMeasurement() error {
	h := newHarness()
	defer h.close()

	deviceID, err := h.deviceWithBaseline("MET-1201")
	if err != nil {
		return err
	}
	opened, err := h.openCase(deviceID)
	if err != nil {
		return err
	}
	caseID, err := nestedString(opened.Body, "case", "id")
	if err != nil {
		return err
	}
	accepted, err := h.recordMeasurement(caseID, 25.2, "Rui")
	if err != nil {
		return err
	}
	if err := expectStatus(accepted, http.StatusCreated); err != nil {
		return err
	}
	state, err := nestedString(accepted.Body, "case", "state")
	if err != nil {
		return err
	}
	if state != "review_ready" {
		return fmt.Errorf("accepted measurement state = %q", state)
	}
	acceptedValue, err := nestedBool(accepted.Body, "measurements", 0, "accepted")
	if err != nil {
		return err
	}
	if !acceptedValue {
		return errors.New("in-tolerance measurement was not accepted")
	}

	second, err := h.recordMeasurement(caseID, 25.1, "Rui")
	if err != nil {
		return err
	}
	if err := expectStatus(second, http.StatusConflict); err != nil {
		return err
	}
	if err := expectErrorCode(second, "state_conflict"); err != nil {
		return err
	}

	rejectDeviceID, err := h.deviceWithBaseline("MET-1202")
	if err != nil {
		return err
	}
	rejectCase, err := h.openCase(rejectDeviceID)
	if err != nil {
		return err
	}
	rejectCaseID, err := nestedString(rejectCase.Body, "case", "id")
	if err != nil {
		return err
	}
	rejected, err := h.recordMeasurement(rejectCaseID, 26.5, "Rui")
	if err != nil {
		return err
	}
	if err := expectStatus(rejected, http.StatusCreated); err != nil {
		return err
	}
	rejectedState, err := nestedString(rejected.Body, "case", "state")
	if err != nil {
		return err
	}
	if rejectedState != "recheck_required" {
		return fmt.Errorf("out-of-tolerance state = %q", rejectedState)
	}
	rejectedValue, err := nestedBool(rejected.Body, "measurements", 0, "accepted")
	if err != nil {
		return err
	}
	if rejectedValue {
		return errors.New("out-of-tolerance measurement was accepted")
	}
	return nil
}

func checkReopenVerificationCase() error {
	h := newHarness()
	defer h.close()

	caseID, err := h.rejectedCase("MET-1301")
	if err != nil {
		return err
	}
	reopened, err := h.request(http.MethodPost, "/cases/"+caseID+"/reopen", map[string]any{
		"actor":  "Lin",
		"reason": "measurement setup was replaced",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(reopened, http.StatusOK); err != nil {
		return err
	}
	state, err := nestedString(reopened.Body, "case", "state")
	if err != nil {
		return err
	}
	if state != "awaiting_measurement" {
		return fmt.Errorf("reopened case state = %q", state)
	}
	measurements, err := nestedSlice(reopened.Body, "measurements")
	if err != nil {
		return err
	}
	if len(measurements) != 1 {
		return fmt.Errorf("reopen changed measurement history: %d entries", len(measurements))
	}

	second, err := h.request(http.MethodPost, "/cases/"+caseID+"/reopen", map[string]any{
		"actor":  "Lin",
		"reason": "duplicate request",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(second, http.StatusConflict); err != nil {
		return err
	}
	if err := expectErrorCode(second, "state_conflict"); err != nil {
		return err
	}

	events, err := h.request(http.MethodGet, "/cases/"+caseID+"/events", nil)
	if err != nil {
		return err
	}
	if err := expectStatus(events, http.StatusOK); err != nil {
		return err
	}
	if !hasEvent(events.Body, "case_reopened") {
		return errors.New("reopen audit event is missing")
	}
	return nil
}

func checkSealVerification() error {
	h := newHarness()
	defer h.close()

	caseID, err := h.readyCase("MET-1401")
	if err != nil {
		return err
	}
	type outcome struct {
		response apiResponse
		err      error
	}
	results := make(chan outcome, 2)
	var wait sync.WaitGroup
	for index := 0; index < 2; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			response, err := h.request(http.MethodPost, "/cases/"+caseID+"/seal", map[string]any{
				"reviewer": fmt.Sprintf("Reviewer-%d", index+1),
				"summary":  "accepted measurement sealed for review",
			})
			results <- outcome{response: response, err: err}
		}(index)
	}
	wait.Wait()
	close(results)

	successes := 0
	conflicts := 0
	for item := range results {
		if item.err != nil {
			return item.err
		}
		switch item.response.Status {
		case http.StatusCreated:
			successes++
		case http.StatusConflict:
			conflicts++
		default:
			return fmt.Errorf("unexpected concurrent seal status %d: %s", item.response.Status, item.response.Raw)
		}
	}
	if successes != 1 || conflicts != 1 {
		return fmt.Errorf("concurrent seal outcomes: %d success, %d conflict", successes, conflicts)
	}

	read, err := h.request(http.MethodGet, "/cases/"+caseID, nil)
	if err != nil {
		return err
	}
	if err := expectStatus(read, http.StatusOK); err != nil {
		return err
	}
	state, err := nestedString(read.Body, "case", "state")
	if err != nil {
		return err
	}
	if state != "sealed" {
		return fmt.Errorf("sealed case state = %q", state)
	}
	sealID, err := nestedString(read.Body, "seal", "id")
	if err != nil {
		return err
	}
	if sealID == "" {
		return errors.New("evidence seal is missing")
	}
	return nil
}

func checkConfigBaseline() error {
	h := newHarness()
	defer h.close()

	// A case cannot open while the device has no effective baseline.
	deviceID, err := h.createDevice("MET-1501")
	if err != nil {
		return err
	}
	blocked, err := h.request(http.MethodPost, "/cases", map[string]any{
		"device_id": deviceID,
		"nominal":   25.0,
		"tolerance": 0.5,
		"steward":   "Qiao",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(blocked, http.StatusConflict); err != nil {
		return err
	}
	if err := expectErrorCode(blocked, "no_effective_baseline"); err != nil {
		return err
	}

	// Register the first baseline; the opened case pins it.
	baseOneTime := time.Now().Add(-3 * time.Hour).UTC()
	baselineOne, err := h.registerBaseline(deviceID, "fw-1.0", baseOneTime)
	if err != nil {
		return err
	}
	duplicate, err := h.request(http.MethodPost, "/devices/"+deviceID+"/config-baselines", map[string]any{
		"firmware_version":  "fw-1.1",
		"fixture":           "fixture-a",
		"correction_params": "offset=0.02",
		"effective_at":      baseOneTime,
		"registered_by":     "Lin",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(duplicate, http.StatusConflict); err != nil {
		return err
	}
	opened, err := h.openCase(deviceID)
	if err != nil {
		return err
	}
	caseID, err := nestedString(opened.Body, "case", "id")
	if err != nil {
		return err
	}
	pinned, err := nestedString(opened.Body, "case", "config_baseline_id")
	if err != nil {
		return err
	}
	if pinned != baselineOne {
		return fmt.Errorf("opened case pinned %q, expected %q", pinned, baselineOne)
	}
	status, err := nestedString(opened.Body, "config_status")
	if err != nil {
		return err
	}
	if status != "current" {
		return fmt.Errorf("opened case config status = %q", status)
	}

	// An out-of-tolerance measurement is decided under baseline one.
	rejected, err := h.recordMeasurement(caseID, 26.5, "Rui")
	if err != nil {
		return err
	}
	if err := expectStatus(rejected, http.StatusCreated); err != nil {
		return err
	}
	decidedWith, err := nestedStringAt(rejected.Body, "measurements", 0, "config_baseline_id")
	if err != nil {
		return err
	}
	if decidedWith != baselineOne {
		return fmt.Errorf("measurement decided with %q, expected %q", decidedWith, baselineOne)
	}

	// A newer baseline takes effect while the case is unfinished.
	baselineTwo, err := h.registerBaseline(deviceID, "fw-2.0", time.Now().Add(-2*time.Hour))
	if err != nil {
		return err
	}
	drifted, err := h.request(http.MethodGet, "/cases/"+caseID, nil)
	if err != nil {
		return err
	}
	driftStatus, err := nestedString(drifted.Body, "config_status")
	if err != nil {
		return err
	}
	if driftStatus != "superseded" {
		return fmt.Errorf("drifted case config status = %q", driftStatus)
	}

	// Progress is blocked until the drift is explicitly handled.
	blockedReopen, err := h.request(http.MethodPost, "/cases/"+caseID+"/reopen", map[string]any{
		"actor":  "Lin",
		"reason": "retry after fixture check",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(blockedReopen, http.StatusConflict); err != nil {
		return err
	}
	if err := expectErrorCode(blockedReopen, "unresolved_config_drift"); err != nil {
		return err
	}
	invalidAction, err := h.request(http.MethodPost, "/cases/"+caseID+"/config-resolution", map[string]any{
		"actor":  "Lin",
		"action": "ignore",
		"reason": "not a supported action",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(invalidAction, http.StatusBadRequest); err != nil {
		return err
	}

	// Explicit handling: keep the pinned baseline for this case.
	resolved, err := h.request(http.MethodPost, "/cases/"+caseID+"/config-resolution", map[string]any{
		"actor":  "Lin",
		"action": "keep_pinned",
		"reason": "fixture unchanged for this run",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(resolved, http.StatusOK); err != nil {
		return err
	}
	kept, err := nestedString(resolved.Body, "case", "config_baseline_id")
	if err != nil {
		return err
	}
	if kept != baselineOne {
		return fmt.Errorf("keep_pinned rebased the case to %q", kept)
	}
	acknowledged, err := nestedString(resolved.Body, "case", "acknowledged_config_id")
	if err != nil {
		return err
	}
	if acknowledged != baselineTwo {
		return fmt.Errorf("acknowledged config = %q, expected %q", acknowledged, baselineTwo)
	}
	resolvedAgain, err := h.request(http.MethodPost, "/cases/"+caseID+"/config-resolution", map[string]any{
		"actor":  "Lin",
		"action": "keep_pinned",
		"reason": "no drift remains",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(resolvedAgain, http.StatusConflict); err != nil {
		return err
	}

	// The case proceeds and still decides with the pinned baseline.
	reopened, err := h.request(http.MethodPost, "/cases/"+caseID+"/reopen", map[string]any{
		"actor":  "Lin",
		"reason": "retry after fixture check",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(reopened, http.StatusOK); err != nil {
		return err
	}
	accepted, err := h.recordMeasurement(caseID, 25.2, "Rui")
	if err != nil {
		return err
	}
	if err := expectStatus(accepted, http.StatusCreated); err != nil {
		return err
	}
	secondDecision, err := nestedStringAt(accepted.Body, "measurements", 1, "config_baseline_id")
	if err != nil {
		return err
	}
	if secondDecision != baselineOne {
		return fmt.Errorf("second measurement decided with %q, expected %q", secondDecision, baselineOne)
	}

	// Another baseline change; this time the steward adopts it.
	baselineThree, err := h.registerBaseline(deviceID, "fw-3.0", time.Now().Add(-time.Hour))
	if err != nil {
		return err
	}
	blockedSeal, err := h.request(http.MethodPost, "/cases/"+caseID+"/seal", map[string]any{
		"reviewer": "Mira",
		"summary":  "seal during drift must be blocked",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(blockedSeal, http.StatusConflict); err != nil {
		return err
	}
	if err := expectErrorCode(blockedSeal, "unresolved_config_drift"); err != nil {
		return err
	}
	adopted, err := h.request(http.MethodPost, "/cases/"+caseID+"/config-resolution", map[string]any{
		"actor":  "Lin",
		"action": "adopt_current",
		"reason": "firmware update verified for this case",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(adopted, http.StatusOK); err != nil {
		return err
	}
	rebased, err := nestedString(adopted.Body, "case", "config_baseline_id")
	if err != nil {
		return err
	}
	if rebased != baselineThree {
		return fmt.Errorf("adopt_current pinned %q, expected %q", rebased, baselineThree)
	}

	// History is preserved: earlier measurements still cite baseline one.
	reread, err := h.request(http.MethodGet, "/cases/"+caseID, nil)
	if err != nil {
		return err
	}
	firstDecision, err := nestedStringAt(reread.Body, "measurements", 0, "config_baseline_id")
	if err != nil {
		return err
	}
	if firstDecision != baselineOne {
		return fmt.Errorf("historical measurement rebased to %q", firstDecision)
	}

	// The seal captures the configuration in force at seal time.
	sealed, err := h.request(http.MethodPost, "/cases/"+caseID+"/seal", map[string]any{
		"reviewer": "Mira",
		"summary":  "accepted measurement sealed for review",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(sealed, http.StatusCreated); err != nil {
		return err
	}
	sealedWith, err := nestedString(sealed.Body, "seal", "config_baseline_id")
	if err != nil {
		return err
	}
	if sealedWith != baselineThree {
		return fmt.Errorf("seal recorded config %q, expected %q", sealedWith, baselineThree)
	}

	// Sealed evidence is frozen even when the device config moves on.
	baselineFour, err := h.registerBaseline(deviceID, "fw-4.0", time.Now())
	if err != nil {
		return err
	}
	frozen, err := h.request(http.MethodGet, "/cases/"+caseID, nil)
	if err != nil {
		return err
	}
	frozenStatus, err := nestedString(frozen.Body, "config_status")
	if err != nil {
		return err
	}
	if frozenStatus != "superseded" {
		return fmt.Errorf("sealed case config status = %q", frozenStatus)
	}
	frozenSeal, err := nestedString(frozen.Body, "seal", "config_baseline_id")
	if err != nil {
		return err
	}
	if frozenSeal != baselineThree {
		return fmt.Errorf("sealed evidence rewritten to config %q", frozenSeal)
	}
	frozenDecision, err := nestedStringAt(frozen.Body, "measurements", 0, "config_baseline_id")
	if err != nil {
		return err
	}
	if frozenDecision != baselineOne {
		return fmt.Errorf("sealed measurement history rewritten to %q", frozenDecision)
	}
	sealedResolve, err := h.request(http.MethodPost, "/cases/"+caseID+"/config-resolution", map[string]any{
		"actor":  "Lin",
		"action": "adopt_current",
		"reason": "sealed cases must not change",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(sealedResolve, http.StatusConflict); err != nil {
		return err
	}

	// The case read embeds every baseline its decisions referenced.
	for _, id := range []string{baselineOne, baselineTwo, baselineThree, baselineFour} {
		if !hasBaseline(frozen.Body, id) {
			return fmt.Errorf("case read does not reconstruct baseline %s", id)
		}
	}

	// The event trail exposes the configuration source of each decision.
	events, err := h.request(http.MethodGet, "/cases/"+caseID+"/events", nil)
	if err != nil {
		return err
	}
	if err := expectStatus(events, http.StatusOK); err != nil {
		return err
	}
	if !hasEvent(events.Body, "case_config_resolved") {
		return errors.New("config resolution audit event is missing")
	}
	if !hasEvent(events.Body, "config_baseline_registered") {
		return errors.New("baseline registration audit event is missing")
	}
	items, err := nestedSlice(events.Body, "events")
	if err != nil {
		return err
	}
	for _, item := range items {
		event, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if event["config_baseline_id"] == "" || event["config_baseline_id"] == nil {
			return fmt.Errorf("event %v has no config baseline source", event["kind"])
		}
	}

	// The device read exposes the baseline history and the current one.
	deviceView, err := h.request(http.MethodGet, "/devices/"+deviceID, nil)
	if err != nil {
		return err
	}
	if err := expectStatus(deviceView, http.StatusOK); err != nil {
		return err
	}
	current, err := nestedString(deviceView.Body, "current_config_baseline", "id")
	if err != nil {
		return err
	}
	if current != baselineFour {
		return fmt.Errorf("current device baseline = %q, expected %q", current, baselineFour)
	}
	list, err := h.request(http.MethodGet, "/devices/"+deviceID+"/config-baselines", nil)
	if err != nil {
		return err
	}
	if err := expectStatus(list, http.StatusOK); err != nil {
		return err
	}
	baselines, err := nestedSlice(list.Body, "config_baselines")
	if err != nil {
		return err
	}
	if len(baselines) != 4 {
		return fmt.Errorf("device baseline history has %d entries, expected 4", len(baselines))
	}
	return nil
}

func hasBaseline(root map[string]any, id string) bool {
	items, err := nestedSlice(root, "config_baselines")
	if err != nil {
		return false
	}
	for _, item := range items {
		baseline, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if baseline["id"] == id {
			return true
		}
	}
	return false
}

func newHarness() *harness {
	server := httptest.NewServer(bootstrap.NewHandler())
	return &harness{
		baseURL: server.URL,
		client:  server.Client(),
		server:  server,
	}
}

func (h *harness) close() {
	h.server.Close()
}

func (h *harness) request(method, path string, body any) (apiResponse, error) {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return apiResponse{}, err
		}
		reader = bytes.NewReader(encoded)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, method, h.baseURL+path, reader)
	if err != nil {
		return apiResponse{}, err
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := h.client.Do(request)
	if err != nil {
		return apiResponse{}, err
	}
	defer response.Body.Close()
	rawBytes, err := io.ReadAll(response.Body)
	if err != nil {
		return apiResponse{}, err
	}
	result := apiResponse{Status: response.StatusCode, Raw: string(rawBytes)}
	if len(bytes.TrimSpace(rawBytes)) > 0 {
		if err := json.Unmarshal(rawBytes, &result.Body); err != nil {
			return apiResponse{}, fmt.Errorf("decode %s %s response: %w", method, path, err)
		}
	}
	return result, nil
}

func (h *harness) createDevice(tag string) (string, error) {
	response, err := h.request(http.MethodPost, "/devices", map[string]any{
		"asset_tag": tag,
		"model":     "Bench Meter",
		"room":      "Room A",
		"steward":   "Lin",
	})
	if err != nil {
		return "", err
	}
	if err := expectStatus(response, http.StatusCreated); err != nil {
		return "", err
	}
	return nestedString(response.Body, "device", "id")
}

func (h *harness) registerBaseline(deviceID, firmware string, effectiveAt time.Time) (string, error) {
	response, err := h.request(http.MethodPost, "/devices/"+deviceID+"/config-baselines", map[string]any{
		"firmware_version":  firmware,
		"fixture":           "fixture-a",
		"correction_params": "offset=0.01",
		"effective_at":      effectiveAt.UTC(),
		"registered_by":     "Lin",
	})
	if err != nil {
		return "", err
	}
	if err := expectStatus(response, http.StatusCreated); err != nil {
		return "", err
	}
	return nestedString(response.Body, "config_baseline", "id")
}

func (h *harness) deviceWithBaseline(tag string) (string, error) {
	deviceID, err := h.createDevice(tag)
	if err != nil {
		return "", err
	}
	if _, err := h.registerBaseline(deviceID, "fw-1.0", time.Now().Add(-time.Hour)); err != nil {
		return "", err
	}
	return deviceID, nil
}

func (h *harness) openCase(deviceID string) (apiResponse, error) {
	response, err := h.request(http.MethodPost, "/cases", map[string]any{
		"device_id": deviceID,
		"nominal":   25.0,
		"tolerance": 0.5,
		"steward":   "Qiao",
	})
	if err != nil {
		return apiResponse{}, err
	}
	if err := expectStatus(response, http.StatusCreated); err != nil {
		return apiResponse{}, err
	}
	return response, nil
}

func (h *harness) recordMeasurement(caseID string, observed float64, technician string) (apiResponse, error) {
	return h.request(http.MethodPost, "/cases/"+caseID+"/measurements", map[string]any{
		"observed":   observed,
		"technician": technician,
	})
}

func (h *harness) rejectedCase(tag string) (string, error) {
	deviceID, err := h.deviceWithBaseline(tag)
	if err != nil {
		return "", err
	}
	opened, err := h.openCase(deviceID)
	if err != nil {
		return "", err
	}
	caseID, err := nestedString(opened.Body, "case", "id")
	if err != nil {
		return "", err
	}
	response, err := h.recordMeasurement(caseID, 26.5, "Rui")
	if err != nil {
		return "", err
	}
	if err := expectStatus(response, http.StatusCreated); err != nil {
		return "", err
	}
	return caseID, nil
}

func (h *harness) readyCase(tag string) (string, error) {
	deviceID, err := h.deviceWithBaseline(tag)
	if err != nil {
		return "", err
	}
	opened, err := h.openCase(deviceID)
	if err != nil {
		return "", err
	}
	caseID, err := nestedString(opened.Body, "case", "id")
	if err != nil {
		return "", err
	}
	response, err := h.recordMeasurement(caseID, 25.2, "Rui")
	if err != nil {
		return "", err
	}
	if err := expectStatus(response, http.StatusCreated); err != nil {
		return "", err
	}
	return caseID, nil
}

func expectStatus(response apiResponse, expected int) error {
	if response.Status != expected {
		return fmt.Errorf("status = %d, expected %d: %s", response.Status, expected, response.Raw)
	}
	return nil
}

func expectErrorCode(response apiResponse, expected string) error {
	code, err := nestedString(response.Body, "error", "code")
	if err != nil {
		return err
	}
	if code != expected {
		return fmt.Errorf("error code = %q, expected %q", code, expected)
	}
	return nil
}

func nestedString(root map[string]any, path ...string) (string, error) {
	value, err := nestedValue(root, path...)
	if err != nil {
		return "", err
	}
	text, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("%s is not a string", strings.Join(path, "."))
	}
	return text, nil
}

func nestedStringAt(root map[string]any, path ...any) (string, error) {
	value, err := nestedAny(root, path...)
	if err != nil {
		return "", err
	}
	text, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("%v is not a string", path)
	}
	return text, nil
}

func nestedBool(root map[string]any, path ...any) (bool, error) {
	value, err := nestedAny(root, path...)
	if err != nil {
		return false, err
	}
	flag, ok := value.(bool)
	if !ok {
		return false, fmt.Errorf("%v is not a boolean", path)
	}
	return flag, nil
}

func nestedSlice(root map[string]any, key string) ([]any, error) {
	value, ok := root[key]
	if !ok {
		return nil, fmt.Errorf("missing %s", key)
	}
	items, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("%s is not an array", key)
	}
	return items, nil
}

func hasEvent(root map[string]any, kind string) bool {
	items, err := nestedSlice(root, "events")
	if err != nil {
		return false
	}
	for _, item := range items {
		event, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if event["kind"] == kind {
			return true
		}
	}
	return false
}

func nestedValue(root map[string]any, path ...string) (any, error) {
	parts := make([]any, 0, len(path))
	for _, part := range path {
		parts = append(parts, part)
	}
	return nestedAny(root, parts...)
}

func nestedAny(root map[string]any, path ...any) (any, error) {
	var current any = root
	for _, part := range path {
		switch typed := part.(type) {
		case string:
			object, ok := current.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("%s is not an object", typed)
			}
			value, ok := object[typed]
			if !ok {
				return nil, fmt.Errorf("missing %s", typed)
			}
			current = value
		case int:
			items, ok := current.([]any)
			if !ok {
				return nil, fmt.Errorf("index %d is not inside an array", typed)
			}
			if typed < 0 || typed >= len(items) {
				return nil, fmt.Errorf("index %d is out of range", typed)
			}
			current = items[typed]
		default:
			return nil, fmt.Errorf("unsupported path element %T", part)
		}
	}
	return current, nil
}
