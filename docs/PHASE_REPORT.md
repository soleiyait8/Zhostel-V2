# Phase report

| Phase | Scope | Status |
|---|---|---|
| 1 | Database, authentication, employee management, dashboard | Complete |
| 2 | ADMS protocol adapter and transaction ingestion | Complete; awaiting physical-device verification |
| 3 | Attendance engine, shifts, DTR, reports | Not started (schema ready) |
| 4 | Security hardening, Docker deployment, monitoring, backup/restore | Complete; Docker images not yet built on a real host |
| 5 | Physical-device integration and production acceptance | Requires the MB10-VL; procedure written |

## Phase 1 — database, authentication, employees, dashboard

**Files**

- `backend/internal/db/migrations/0001_init.sql` — every table in the brief, plus `sessions`, `device_users`, `device_communication_logs`, `sync_error_logs`, `device_commands`, `holidays`, `app_config`
- `backend/internal/db/db.go` — connection with retry; migration runner (advisory-locked, one transaction per migration)
- `backend/internal/auth/` — PBKDF2-SHA256 hashing, server-side sessions, role checks, CSRF header check
- `backend/internal/audit/audit.go` — audit writer, called inside the same transaction as each change
- `backend/internal/api/server.go`, `employees.go`, `dashboard.go` — auth, users, departments, employees, CSV, mappings, punches, dashboard, audit
- `backend/cmd/api`, `backend/cmd/migrate`
- `frontend/` — login, Today, Employees (list, detail, CSV import/export, mappings), Punch history, Audit trail, Users

**Database-enforced rules**

- One device user ID maps to at most one employee at any instant (`EXCLUDE USING gist` on device, user ID and validity range). Mappings are closed, never deleted.
- `raw_attendance_logs`: original payload, hash, device timestamp, serial and received time cannot be changed; rows cannot be deleted.
- `audit_logs`: cannot be updated or deleted.
- Shift assignments cannot overlap for one employee.
- An adjustment cannot be approved by the person who requested it.
- All durations are `integer` minutes; all instants are `timestamptz`.

## Phase 2 — ADMS protocol adapter and ingestion

**Files**

- `backend/internal/protocol/protocol.go` — request classification, serial and user ID validation, ATTLOG and USER parsing, line-ending and BOM normalisation, biometric redaction, command results
- `backend/internal/protocol/profile.go` — every firmware-dependent reply detail, set from environment variables
- `backend/internal/adms/server.go` — the device-facing service
- `backend/cmd/adms`, `backend/cmd/simdevice`
- Device pages in the frontend: status, setup instructions, user ID mapping, request log, open problems, reprocessing

**Behaviour**

- Upload acknowledged only after commit; database failure returns 503 and nothing partial is stored.
- Idempotent by SHA-256 of serial and line; replays are acknowledged without duplication.
- Unknown and disabled devices refused and alerted; repeats collapse into one alert with a counter.
- Optional per-device allowed networks.
- Each request logged with redacted body, reply and outcome; heartbeats not logged unless enabled; log pruned after 30 days.
- Malformed lines and punches dated more than a day ahead are stored as held records with a reason, and can be re-evaluated by an administrator.
- Device timezone per device; the original wall-clock time is kept beside the computed instant.
- Command queue implemented but disabled by default.

## Phase 4 — security hardening and deployment

**Files**

- `backend/Dockerfile` — static Go binaries on distroless, non-root
- `frontend/Dockerfile` — Next.js standalone, non-root, health check
- `backend/cmd/healthcheck` — health probe for images without a shell
- `deploy/docker-compose.yml` — postgres, migrate (one-shot), adms, api, frontend, caddy
- `deploy/caddy/Caddyfile` — two hostnames, automatic TLS, route isolation, body limits, HSTS
- `deploy/.env.example` — placeholders only
- `deploy/scripts/backup.sh`, `restore.sh`, `create-admin.sh`, `simulate-device.sh`
- `docs/` — DEPLOYMENT, DEVICE_GUIDE, PROTOCOL_NOTES, SECURITY, TROUBLESHOOTING, openapi.yaml

**Hardening**

- Only Caddy publishes ports. PostgreSQL is on a Docker network marked `internal`.
- App containers: read-only root file system, all capabilities dropped, `no-new-privileges`, non-root user.
- Log rotation on every container.
- Secrets only in `.env`, which is git-ignored.

## Setup and run commands

Production: `docs/DEPLOYMENT.md`. Development:

```bash
cd backend
export DATABASE_URL="postgres://localhost/adms_dev?sslmode=disable" APP_ENV=development \
       COOKIE_SECURE=false TRUST_PROXY=false ADMIN_PASSWORD="choose a long password"
go run ./cmd/migrate -create-admin you@example.com -seed-dev
go run ./cmd/adms & go run ./cmd/api &
go run ./cmd/simdevice
cd ../frontend && npm ci && npm run dev
```

## Tests and results

Run on 19 October 2026 with Go 1.24.13, PostgreSQL 16 and Node 22: **32 passed, 0 failed**; frontend type check and production build pass.

| Package | Tests | Covers |
|---|---|---|
| `internal/protocol` | 13 | Endpoint classification; serial validation including injection strings; ATTLOG parsing with CRLF, CR, BOM and blank lines; seven rejection reasons; device timezone conversion across midnight; hash stability; USER parsing; template redaction; handshake format and stamps; both acknowledgement styles; command results; firmware from INFO |
| `internal/adms` (integration) | 11 | Unknown device not acknowledged and alert counted; invalid serials; handshake and heartbeat updating device state; ingestion with accepted and held lines; replay not duplicated; stamp echoed; punch instant correct; **database failure not acknowledged, nothing partial stored, recovery on retry**; future timestamps held; disabled device and allowed networks; user list stored with templates kept out of the log; raw records immutable and undeletable; command queue round trip; oversize body |
| `internal/api` (integration) | 5 | Anonymous, viewer, HR and admin permissions; missing CSRF header; logout; employee validation (three field errors); overlapping mapping refused naming the holder; deactivation closing mappings and allowing reuse with history kept; audit entries written and undeletable; CSV dry run, import, all-or-nothing failure, formula neutralisation; punches attributed to the employee mapped at punch time across a mapping change; reprocessing; dashboard counts; device status; unmatched IDs; reachability reporting a dead service honestly |
| `internal/auth` | 3 | Password round trip and malformed hashes; password policy; role ranking including unknown roles |

**Additional verification beyond the automated tests**

- End to end with compiled binaries: migrate, seed, both services, simulated device, dashboard queries.
- Through the real Caddy 2.8.4 binary with the production Caddyfile: dashboard pages, static assets and API on the app host; `/iclock/*` refused there (404); device heartbeat on the device host; API and health endpoints refused there (404); 3 MB device upload refused (413); session cookie HttpOnly; client IP passed correctly through the proxy; HSTS, CSP and frame-blocking headers present.
- `caddy validate` on the production Caddyfile.
- OpenAPI 3.1 spec validated, and checked against the code's route table: no route missing in either direction.
- Backup and restore commands exercised on a live database: dump verified, deliberate damage reverted by restore, immutability trigger and overlap constraint intact afterwards. This exposed a real defect (the restore tried to drop an extension the application user does not own and silently rolled back), which is fixed in `restore.sh`.

## Known limitations

1. **Protocol unverified on the MB10-VL.** Defaults follow the generic PUSH documentation. The ADMS service logs a warning until `ADMS_PROFILE_VERIFIED=true`.
2. **Status and verify codes are stored raw.** Their meaning must be mapped from a capture before the attendance engine uses them.
3. **Docker images not built here.** The build environment had no Docker. The Compose file was validated structurally, and every service was run as a native binary behind the real Caddy. The first `docker compose up --build` on the server is the first container build.
4. **HTTPS from the device is unknown.** If the firmware cannot do it, use the on-site relay in `SECURITY.md`.
5. **No Phase 3 features.** Late, missing-punch and overtime figures show as unavailable; there is no DTR, adjustment workflow, leave screen or PDF/Excel export yet.
6. **Rate limiters are in-memory.** Correct for one replica per service, which is how the stack is defined.
7. **Dashboard not reviewed in a real browser in this environment.** It builds and type-checks; review it on desktop and phone during acceptance.

## Phase 3 — next

Build in this order, each with tests:

1. **Configuration for device codes.** Store the status and verify code mapping from the Phase 5 capture in `app_config`.
2. **Calculation engine** as a pure Go package (`internal/attendance`) taking punches, shift, holidays and leave for one employee and one attendance date, and returning an `attendance_days` row plus flags. Include:
   - shift-aware day boundaries: an overnight shift's punches belong to the date the shift starts;
   - punch pairing: use verified direction codes when present, otherwise first-in and last-out within the shift window with configurable tolerance, and flag ambiguity for HR review;
   - duplicate collapsing within a configurable window (for example 2 minutes);
   - grace period, lateness, undertime, breaks and overtime rules;
   - holidays, rest days and approved leave.
   Test: day shift, overnight shift, duplicate punches, missed in, missed out, multiple breaks, punch exactly on the grace boundary, leave day, holiday.
3. **Recalculation worker.** Recalculates affected dates after new punches, mapping changes, shift changes and approved adjustments. Make it idempotent and record `calculation_version`.
4. **Shifts, assignments, holidays and leave** screens and endpoints.
5. **Adjustment workflow**: request, approval by a different user, audit and recalculation. Raw logs are never touched.
6. **Reports**: daily attendance, monthly DTR, late and undertime, absences, overtime, department summary, device logs, adjustments. CSV first, then Excel, then PDF. All with date, employee, department and location filters.
7. **Dashboard**: switch late arrivals, missing punches and overtime from unavailable to live figures.

## Phase 5 — production acceptance

Follow the checklist in `docs/PROTOCOL_NOTES.md` with the physical MB10-VL. Commit the captured payloads as test fixtures, record the code mappings and firmware version, then set `ADMS_PROFILE_VERIFIED=true`.
