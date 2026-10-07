# Metrology Assurance Suite

Metrology Assurance Suite is a Go HTTP service for bounded laboratory
measurement assurance work. It registers devices, tracks immutable device
configuration baselines, opens verification cases pinned to the configuration
in effect, records measurements, handles failed tolerance decisions, and seals
traceable evidence records.

## Configuration Baselines

Firmware versions, fixtures, and correction parameters change while a device
is in service. The steward registers each configuration as a baseline with a
version label and an effective time (`POST /devices/{id}/configurations`).
Baselines are append-only: versions are unique per device and effective times
must move forward, so history cannot be rewritten retroactively.

Opening a case pins the baseline in effect at that moment. Every measurement
and the evidence seal record the baseline they were judged under, and the case
view exposes the pinned baseline, the currently effective baseline, and a
`config_drifted` flag. When a newer baseline takes effect during an unfinished
case, measurements and sealing fail with `config_drift` until the steward
explicitly rebaselines the case (`POST /cases/{id}/rebaseline`), which returns
it to `awaiting_measurement`. Sealed cases keep their pinned baseline forever
and can never be rebaselined.

## Run

```bash
go run ./cmd/metrology-service --addr 127.0.0.1:18121
```

The service listens on `127.0.0.1:18121` by default.

## Build And Verify

```bash
go build ./...
go run ./cmd/workflowcheck --workflow register-device
go run ./cmd/workflowcheck --workflow config-baseline
go run ./cmd/workflowcheck --workflow open-verification-case
go run ./cmd/workflowcheck --workflow record-measurement
go run ./cmd/workflowcheck --workflow reopen-verification-case
go run ./cmd/workflowcheck --workflow seal-verification
```

The workflow runner starts a fresh in-process HTTP service and exercises the
public API. It is a production smoke command, not a unit test.

Tests are intentionally deferred for this initialization baseline. The later
engineering-task stage will add the unit and integration test suite and replace
or extend these smoke checks with red/green verification tests.

## API

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

## Layout

- `cmd/metrology-service`: executable HTTP server.
- `cmd/workflowcheck`: bounded workflow verification commands.
- `internal/httpapi`: route and JSON boundary.
- `internal/assurance`: workflow orchestration.
- `internal/domain`: state rules and invariants.
- `internal/repository`: copy-on-write in-memory persistence.
- `internal/contracts`: API request and response models.
- `internal/bootstrap`: concrete dependency wiring.

## Inputs And Outputs

Inputs are JSON HTTP requests. Outputs are JSON response envelopes with either a
resource view or a stable error object:

```json
{
  "error": {
    "code": "state_conflict",
    "message": "case is not awaiting measurement"
  }
}
```

The service has no environment variables and no external dependencies.
