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
- ConfigBaseline: an immutable device configuration version (firmware, fixture,
  and correction parameters) with an effective time and the steward who
  registered it. Baselines are append-only and their effective times strictly
  increase per device.
- VerificationCase: one bounded assurance activity for a device, with a nominal
  value, tolerance, current state, version, assigned steward, and the
  configuration baseline pinned when the case was opened.
- Measurement: an observed value, computed deviation, acceptance decision,
  technician, timestamp, and the configuration baseline the decision relied on.
- EvidenceSeal: the immutable closing record for a reviewed case, including the
  selected accepted measurement and the configuration baseline it was judged
  under.
- AuditEvent: an append-only trace of meaningful case and device changes,
  carrying the configuration baseline reference whenever one is involved.

## Workflows

### 1. Register Device

The steward sends `POST /devices` with an asset tag, model, room, and steward.
The service validates the request, normalizes the asset tag, rejects duplicate
tags, and creates an active device.

### 2. Register Configuration Baseline

The steward sends `POST /devices/{id}/configurations` with a configuration
version, an effective time, and their identity. The device must be active, the
version must be unique for the device, and the effective time must be later
than every baseline already registered for the device, so the configuration
timeline can never be rewritten retroactively. If an unfinished case exists
and the new baseline is already in effect, the service also records a
`config_drift_pending` audit event on that case.

### 3. Open Verification Case

The steward sends `POST /cases` with a device ID, nominal value, tolerance, and
owner. The service verifies that the device exists, is active, has no
unfinished case, and has an effective configuration baseline. A valid request
creates a case in `awaiting_measurement` pinned to that baseline.

### 4. Record Measurement

A technician sends `POST /cases/{id}/measurements`. The case must be
`awaiting_measurement` and its pinned baseline must still be the effective
one; otherwise the request fails with `config_drift`. The service computes the
signed deviation from the nominal value and stores the pinned baseline on the
measurement. An in-tolerance measurement moves the case to
`review_ready`; an out-of-tolerance measurement moves it to
`recheck_required`.

### 5. Reopen Verification Case

The steward sends `POST /cases/{id}/reopen` after an out-of-tolerance result.
Only `recheck_required` cases may move back to `awaiting_measurement`. The
service records the actor and reason in the audit trail.

### 6. Rebaseline Verification Case

When a newer configuration baseline has taken effect during an unfinished
case, the steward sends `POST /cases/{id}/rebaseline` with an actor and
reason. The service pins the case to the currently effective baseline, returns
the case to `awaiting_measurement` so measurements are taken under the new
configuration, and records the change in the audit trail. Historical
measurements keep the baseline they were recorded under. A case that is
already aligned, or already sealed, cannot be rebaselined.

### 7. Seal Verification

The verifier sends `POST /cases/{id}/seal` after an accepted measurement. Only
`review_ready` cases whose pinned baseline is still effective may be sealed;
a drifted case fails with `config_drift`. The service selects the accepted
measurement with the smallest absolute deviation, creates exactly one evidence
seal carrying the pinned baseline, moves the case to `sealed`, and rejects
later mutations.

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
- Configuration baseline versions are unique per device, and their effective
  times strictly increase so the timeline is append-only.
- Opening a case requires an effective configuration baseline and pins it.
- Measurements are accepted only while a case is awaiting measurement and its
  pinned baseline is still the effective one.
- A measurement, seal, and judgment audit event each record the configuration
  baseline they relied on.
- A drifted unfinished case must be explicitly rebaselined before further
  measurements or sealing; rebaselining returns it to `awaiting_measurement`.
- A tolerance must be positive.
- A case can have at most one evidence seal.
- A sealed case cannot accept measurements, reopen, rebaseline, or seal again,
  and later baselines never rewrite sealed evidence.
- Every mutation that changes a case also appends an audit event in the same
  repository commit.

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
- `POST /devices/{id}/configurations`
- `GET /devices/{id}/configurations`
- `POST /cases`
- `GET /cases/{id}`
- `POST /cases/{id}/measurements`
- `POST /cases/{id}/reopen`
- `POST /cases/{id}/rebaseline`
- `POST /cases/{id}/seal`
- `GET /cases/{id}/events`

## Validation Plan

The baseline build command is `go build ./...`. Each declared workflow has a
bounded production smoke command under `cmd/workflowcheck`. The command starts
an HTTP test server in the same process, calls the public routes, and verifies
both success and failure behavior. The seal check also races two concurrent
seal requests to prove that the repository mutation lock permits exactly one
seal. The config-baseline check walks the full configuration lifecycle:
registration, pinning at case opening, drift blocking, explicit rebaselining,
and the immutability of sealed evidence.

Tests are intentionally deferred for this initialization baseline. The later
engineering-task stage will add unit and integration tests, including red/green
verification for the workflows and their failure paths.
