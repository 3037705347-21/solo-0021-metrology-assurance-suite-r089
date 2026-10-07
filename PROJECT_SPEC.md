# Metrology Assurance Suite

## Project Goal

Metrology Assurance Suite is a local HTTP service that keeps a traceable record
of laboratory measurement assurance work. It registers devices, opens a
verification case for one device, records technician measurements, requests a
repeat measurement when tolerance is exceeded, and seals the final evidence
record after review.

The service is intentionally self-contained. It uses an in-memory repository
with atomic copy-on-write mutations and does not require a database or an
external service.

## Users

- Laboratory steward: registers devices and opens verification cases.
- Measurement technician: records observed values for an open case.
- Quality verifier: reviews accepted measurements and seals the case.

## Core Entities

- Device: a laboratory asset with a unique asset tag, model, room, steward, and
  lifecycle state.
- ConfigBaseline: an immutable device configuration record (firmware version,
  fixture, correction parameters) registered by the steward with an effective
  time. The baseline in force at any moment is the latest one whose effective
  time has passed.
- VerificationCase: one bounded assurance activity for a device, with a nominal
  value, tolerance, current state, version, assigned steward, and the config
  baseline pinned when the case opened.
- Measurement: an observed value, computed deviation, acceptance decision,
  technician, timestamp, and the config baseline the decision relied on.
- EvidenceSeal: the immutable closing record for a reviewed case, including the
  selected accepted measurement and the config baseline in force at seal time.
- AuditEvent: an append-only trace of meaningful case and device changes,
  carrying the config baseline each decision was made under.

## Workflows

### 1. Register Device

The steward sends `POST /devices` with an asset tag, model, room, and steward.
The service validates the request, normalizes the asset tag, rejects duplicate
tags, and creates an active device.

### 2. Open Verification Case

The steward sends `POST /cases` with a device ID, nominal value, tolerance, and
owner. The service verifies that the device exists, is active, and has no
unfinished case. The device must have an effective config baseline; the new
case pins that baseline so every later decision can name its configuration
source. A valid request creates a case in `awaiting_measurement`.

### 3. Record Measurement

A technician sends `POST /cases/{id}/measurements`. The case must be
`awaiting_measurement` and must have no unresolved config drift. The service
computes the signed deviation from the nominal value. An in-tolerance
measurement moves the case to `review_ready`; an out-of-tolerance measurement
moves it to `recheck_required`. The measurement records the config baseline it
was decided under.

### 4. Reopen Verification Case

The steward sends `POST /cases/{id}/reopen` after an out-of-tolerance result.
Only `recheck_required` cases may move back to `awaiting_measurement`. The
service records the actor and reason in the audit trail.

### 5. Seal Verification

The verifier sends `POST /cases/{id}/seal` after an accepted measurement. Only
`review_ready` cases with no unresolved config drift may be sealed. The service
selects the accepted measurement with the smallest absolute deviation, creates
exactly one evidence seal recording the config baseline in force at seal time,
moves the case to `sealed`, and rejects later mutations.

### 6. Register Config Baseline

The steward sends `POST /devices/{id}/config-baselines` with a firmware
version, fixture, correction parameters, an effective time, and a registrar.
The device must exist and be active. Baselines are immutable once registered,
and one device cannot hold two baselines with the same effective time. The
registration is appended to the audit trail and to the timeline of the
device's unfinished case, if one exists.

### 7. Resolve Case Config Drift

When a newer baseline takes effect while a case is unfinished, the case reads
as `config_status: superseded` and measurements, reopens, and seals are
rejected with `unresolved_config_drift`. The steward sends
`POST /cases/{id}/config-resolution` with an actor, a reason, and an action:
`keep_pinned` acknowledges the divergent baseline and continues with the
pinned one, while `adopt_current` re-pins the case to the baseline now in
effect. Both actions are audited; sealed cases cannot be resolved or otherwise
modified.

## State And Rules

Device state:

- `active -> retired`
- `retired` is terminal.

VerificationCase state:

- `awaiting_measurement -> review_ready`
- `awaiting_measurement -> recheck_required`
- `recheck_required -> awaiting_measurement`
- `review_ready -> sealed`
- `sealed` is terminal.

Required invariants:

- Asset tags are unique after case-insensitive normalization.
- A device has at most one unfinished verification case.
- Measurements are accepted only while a case is awaiting measurement.
- A tolerance must be positive.
- A case can have at most one evidence seal.
- A sealed case cannot accept measurements, reopen, resolve config, or seal
  again.
- Every mutation that changes a case also appends an audit event in the same
  repository commit.
- Config baselines are immutable once registered, and a device cannot hold two
  baselines with the same effective time.
- A case pins the config baseline effective at open time and cannot open while
  the device has no effective baseline.
- A measurement, reopen, or seal is rejected while the case has unresolved
  config drift; drift is resolved only by an explicit audited decision.
- Measurements, seals, and case events record the config baseline they were
  decided under, and sealed evidence is never rewritten.

## Modules

- `cmd/metrology-service`: process entrypoint and graceful HTTP shutdown.
- `cmd/workflowcheck`: bounded smoke checks that call the public HTTP API.
- `internal/httpapi`: routing, request decoding, and stable error mapping.
- `internal/assurance`: workflow orchestration and atomic decisions.
- `internal/domain`: entities, validation, state transitions, and errors.
- `internal/repository`: mutex-protected in-memory persistence with copy-on-write
  commits.
- `internal/contracts`: request and response contracts shared by the HTTP layer.

## Public Interfaces

- `GET /healthz`
- `POST /devices`
- `GET /devices/{id}`
- `POST /devices/{id}/config-baselines`
- `GET /devices/{id}/config-baselines`
- `POST /cases`
- `GET /cases/{id}`
- `POST /cases/{id}/measurements`
- `POST /cases/{id}/reopen`
- `POST /cases/{id}/config-resolution`
- `POST /cases/{id}/seal`
- `GET /cases/{id}/events`

## Validation Plan

The baseline build command is `go build ./...`. Each declared workflow has a
bounded production smoke command under `cmd/workflowcheck`. The command starts
an HTTP test server in the same process, calls the public routes, and verifies
both success and failure behavior. The seal check also races two concurrent
seal requests to prove that the repository mutation lock permits exactly one
seal. The config-baseline check proves that a case pins its baseline at open
time, that mid-case configuration changes block progress until an explicit
resolution, and that sealed evidence keeps its recorded configuration.

Tests are intentionally deferred for this initialization baseline. The later
engineering-task stage will add unit and integration tests, including red/green
verification for the workflows and their failure paths.
