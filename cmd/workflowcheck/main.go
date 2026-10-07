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
		"config-baseline":          checkConfigBaseline,
		"open-verification-case":   checkOpenVerificationCase,
		"record-measurement":       checkRecordMeasurement,
		"reopen-verification-case": checkReopenVerificationCase,
		"seal-verification":        checkSealVerification,
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

func checkConfigBaseline() error {
	h := newHarness()
	defer h.close()

	deviceID, err := h.createDevice("MET-1501")
	if err != nil {
		return err
	}

	missing, err := h.request(http.MethodPost, "/cases", map[string]any{
		"device_id": deviceID,
		"nominal":   25.0,
		"tolerance": 0.5,
		"steward":   "Qiao",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(missing, http.StatusConflict); err != nil {
		return err
	}
	if err := expectErrorCode(missing, "config_baseline_missing"); err != nil {
		return err
	}

	base := time.Now().Add(-2 * time.Hour)
	first, err := h.registerBaseline(deviceID, "fw-1.0.0/corr-A", base)
	if err != nil {
		return err
	}
	firstID, err := nestedString(first.Body, "baseline", "id")
	if err != nil {
		return err
	}

	duplicate, err := h.request(http.MethodPost, "/devices/"+deviceID+"/configurations", map[string]any{
		"version":      "fw-1.0.0/corr-A",
		"effective_at": time.Now().UTC().Format(time.RFC3339Nano),
		"steward":      "Lin",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(duplicate, http.StatusConflict); err != nil {
		return err
	}
	if err := expectErrorCode(duplicate, "duplicate_config_version"); err != nil {
		return err
	}

	regression, err := h.request(http.MethodPost, "/devices/"+deviceID+"/configurations", map[string]any{
		"version":      "fw-0.9.0/corr-Z",
		"effective_at": base.Add(-time.Hour).UTC().Format(time.RFC3339Nano),
		"steward":      "Lin",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(regression, http.StatusConflict); err != nil {
		return err
	}
	if err := expectErrorCode(regression, "config_baseline_regression"); err != nil {
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
	pinned, err := nestedString(opened.Body, "case", "baseline_id")
	if err != nil {
		return err
	}
	if pinned != firstID {
		return fmt.Errorf("case pinned baseline %q, expected %q", pinned, firstID)
	}
	drifted, err := nestedBool(opened.Body, "config_drifted")
	if err != nil {
		return err
	}
	if drifted {
		return errors.New("freshly opened case must not report config drift")
	}

	aligned, err := h.request(http.MethodPost, "/cases/"+caseID+"/rebaseline", map[string]any{
		"actor":  "Lin",
		"reason": "no drift has occurred",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(aligned, http.StatusConflict); err != nil {
		return err
	}
	if err := expectErrorCode(aligned, "state_conflict"); err != nil {
		return err
	}

	second, err := h.registerBaseline(deviceID, "fw-2.0.0/corr-B", base.Add(time.Hour))
	if err != nil {
		return err
	}
	secondID, err := nestedString(second.Body, "baseline", "id")
	if err != nil {
		return err
	}

	blocked, err := h.recordMeasurement(caseID, 25.2, "Rui")
	if err != nil {
		return err
	}
	if err := expectStatus(blocked, http.StatusConflict); err != nil {
		return err
	}
	if err := expectErrorCode(blocked, "config_drift"); err != nil {
		return err
	}

	view, err := h.request(http.MethodGet, "/cases/"+caseID, nil)
	if err != nil {
		return err
	}
	if err := expectStatus(view, http.StatusOK); err != nil {
		return err
	}
	drifted, err = nestedBool(view.Body, "config_drifted")
	if err != nil {
		return err
	}
	if !drifted {
		return errors.New("case should report config drift after a newer baseline takes effect")
	}
	current, err := nestedString(view.Body, "current_baseline", "id")
	if err != nil {
		return err
	}
	if current != secondID {
		return fmt.Errorf("current baseline %q, expected %q", current, secondID)
	}

	rebaselined, err := h.request(http.MethodPost, "/cases/"+caseID+"/rebaseline", map[string]any{
		"actor":  "Lin",
		"reason": "firmware updated during the case; re-measuring under the new baseline",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(rebaselined, http.StatusOK); err != nil {
		return err
	}
	pinned, err = nestedString(rebaselined.Body, "case", "baseline_id")
	if err != nil {
		return err
	}
	if pinned != secondID {
		return fmt.Errorf("rebaselined case pinned %q, expected %q", pinned, secondID)
	}
	drifted, err = nestedBool(rebaselined.Body, "config_drifted")
	if err != nil {
		return err
	}
	if drifted {
		return errors.New("rebaselined case must not report config drift")
	}

	measured, err := h.recordMeasurement(caseID, 25.2, "Rui")
	if err != nil {
		return err
	}
	if err := expectStatus(measured, http.StatusCreated); err != nil {
		return err
	}
	judged, err := nestedStringPath(measured.Body, "measurements", 0, "baseline_id")
	if err != nil {
		return err
	}
	if judged != secondID {
		return fmt.Errorf("measurement baseline %q, expected %q", judged, secondID)
	}

	third, err := h.registerBaseline(deviceID, "fw-3.0.0/corr-C", base.Add(90*time.Minute))
	if err != nil {
		return err
	}
	thirdID, err := nestedString(third.Body, "baseline", "id")
	if err != nil {
		return err
	}

	blockedSeal, err := h.request(http.MethodPost, "/cases/"+caseID+"/seal", map[string]any{
		"reviewer": "Wei",
		"summary":  "attempted while the configuration had drifted",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(blockedSeal, http.StatusConflict); err != nil {
		return err
	}
	if err := expectErrorCode(blockedSeal, "config_drift"); err != nil {
		return err
	}

	rebaselined, err = h.request(http.MethodPost, "/cases/"+caseID+"/rebaseline", map[string]any{
		"actor":  "Lin",
		"reason": "correction parameters revised; the earlier reading predates them",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(rebaselined, http.StatusOK); err != nil {
		return err
	}
	state, err := nestedString(rebaselined.Body, "case", "state")
	if err != nil {
		return err
	}
	if state != "awaiting_measurement" {
		return fmt.Errorf("rebaselined review case state = %q", state)
	}
	kept, err := nestedStringPath(rebaselined.Body, "measurements", 0, "baseline_id")
	if err != nil {
		return err
	}
	if kept != secondID {
		return fmt.Errorf("historical measurement baseline rewritten to %q", kept)
	}

	measured, err = h.recordMeasurement(caseID, 25.1, "Rui")
	if err != nil {
		return err
	}
	if err := expectStatus(measured, http.StatusCreated); err != nil {
		return err
	}

	sealed, err := h.request(http.MethodPost, "/cases/"+caseID+"/seal", map[string]any{
		"reviewer": "Wei",
		"summary":  "sealed under configuration fw-3.0.0/corr-C",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(sealed, http.StatusCreated); err != nil {
		return err
	}
	sealBaseline, err := nestedString(sealed.Body, "seal", "baseline_id")
	if err != nil {
		return err
	}
	if sealBaseline != thirdID {
		return fmt.Errorf("seal baseline %q, expected %q", sealBaseline, thirdID)
	}

	if _, err := h.registerBaseline(deviceID, "fw-4.0.0/corr-D", base.Add(100*time.Minute)); err != nil {
		return err
	}
	view, err = h.request(http.MethodGet, "/cases/"+caseID, nil)
	if err != nil {
		return err
	}
	if err := expectStatus(view, http.StatusOK); err != nil {
		return err
	}
	pinned, err = nestedString(view.Body, "case", "baseline_id")
	if err != nil {
		return err
	}
	if pinned != thirdID {
		return fmt.Errorf("sealed case baseline rewritten to %q", pinned)
	}
	drifted, err = nestedBool(view.Body, "config_drifted")
	if err != nil {
		return err
	}
	if drifted {
		return errors.New("sealed case must never report config drift")
	}
	sealBaseline, err = nestedString(view.Body, "seal", "baseline_id")
	if err != nil {
		return err
	}
	if sealBaseline != thirdID {
		return fmt.Errorf("sealed evidence baseline rewritten to %q", sealBaseline)
	}

	late, err := h.request(http.MethodPost, "/cases/"+caseID+"/rebaseline", map[string]any{
		"actor":  "Lin",
		"reason": "attempted after sealing",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(late, http.StatusConflict); err != nil {
		return err
	}
	if err := expectErrorCode(late, "state_conflict"); err != nil {
		return err
	}

	events, err := h.request(http.MethodGet, "/cases/"+caseID+"/events", nil)
	if err != nil {
		return err
	}
	if err := expectStatus(events, http.StatusOK); err != nil {
		return err
	}
	for _, kind := range []string{"config_drift_pending", "case_rebaselined", "case_sealed"} {
		if !hasEvent(events.Body, kind) {
			return fmt.Errorf("audit event %s is missing", kind)
		}
	}
	if !eventHasBaseline(events.Body, "measurement_accepted") {
		return errors.New("measurement events do not record their configuration baseline")
	}

	list, err := h.request(http.MethodGet, "/devices/"+deviceID+"/configurations", nil)
	if err != nil {
		return err
	}
	if err := expectStatus(list, http.StatusOK); err != nil {
		return err
	}
	baselines, err := nestedSlice(list.Body, "baselines")
	if err != nil {
		return err
	}
	if len(baselines) != 4 {
		return fmt.Errorf("baseline count = %d, expected 4", len(baselines))
	}
	lastEffective := time.Time{}
	for _, item := range baselines {
		object, ok := item.(map[string]any)
		if !ok {
			return errors.New("baseline entry is not an object")
		}
		text, ok := object["effective_at"].(string)
		if !ok {
			return errors.New("baseline effective_at is missing")
		}
		stamp, err := time.Parse(time.RFC3339Nano, text)
		if err != nil {
			return fmt.Errorf("parse effective_at: %w", err)
		}
		if !stamp.After(lastEffective) {
			return errors.New("baselines are not ordered by effective time")
		}
		lastEffective = stamp
	}
	return nil
}

func checkOpenVerificationCase() error {
	h := newHarness()
	defer h.close()

	deviceID, err := h.createConfiguredDevice("MET-1101")
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

	deviceID, err := h.createConfiguredDevice("MET-1201")
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

	rejectDeviceID, err := h.createConfiguredDevice("MET-1202")
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

func (h *harness) createConfiguredDevice(tag string) (string, error) {
	deviceID, err := h.createDevice(tag)
	if err != nil {
		return "", err
	}
	if _, err := h.registerBaseline(deviceID, "fw-1.0.0/corr-A", time.Now().Add(-time.Hour)); err != nil {
		return "", err
	}
	return deviceID, nil
}

func (h *harness) registerBaseline(deviceID, version string, effectiveAt time.Time) (apiResponse, error) {
	response, err := h.request(http.MethodPost, "/devices/"+deviceID+"/configurations", map[string]any{
		"version":      version,
		"note":         "registered by workflowcheck",
		"effective_at": effectiveAt.UTC().Format(time.RFC3339Nano),
		"steward":      "Lin",
	})
	if err != nil {
		return apiResponse{}, err
	}
	if err := expectStatus(response, http.StatusCreated); err != nil {
		return apiResponse{}, err
	}
	return response, nil
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
	deviceID, err := h.createConfiguredDevice(tag)
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
	deviceID, err := h.createConfiguredDevice(tag)
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
	parts := make([]any, 0, len(path))
	for _, part := range path {
		parts = append(parts, part)
	}
	return nestedStringPath(root, parts...)
}

func nestedStringPath(root map[string]any, path ...any) (string, error) {
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

func eventHasBaseline(root map[string]any, kind string) bool {
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
			baseline, ok := event["baseline_id"].(string)
			return ok && baseline != ""
		}
	}
	return false
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
