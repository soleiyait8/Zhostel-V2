# ADMS — attendance and device management for the ZKTeco MB10-VL

Receives punches from MB10-VL terminals over the ADMS (PUSH) protocol, stores them as tamper-evident records, links device user IDs to employees, and gives HR a web dashboard. Deploys to one VPS with Docker Compose and automatic HTTPS.

> **Status.** Phases 1, 2 and 4 are complete and tested against PostgreSQL and simulated device traffic. The protocol adapter is **not yet verified on a physical MB10-VL**; that is Phase 5 and is described in [`docs/PROTOCOL_NOTES.md`](docs/PROTOCOL_NOTES.md). The attendance calculation engine (Phase 3) is not built yet. See [`docs/PHASE_REPORT.md`](docs/PHASE_REPORT.md).

## How it fits together

```
 MB10-VL ──HTTPS──▶ adms.example.com ─┐                  ┌─▶ adms service (Go)  ─┐
                                      ├─▶ Caddy (TLS) ───┤                       ├─▶ PostgreSQL
 HR browser ─HTTPS─▶ attendance.example.com ─┘           ├─▶ api service (Go)   ─┘   (internal network,
                                                         └─▶ frontend (Next.js)       no public port)
```

- **adms** speaks only the device protocol on `/iclock/*`. It runs independently, so punches keep arriving when nobody is logged in or the API is being updated.
- **api** is the authenticated application API used by the dashboard. It never serves device routes.
- **frontend** is the HR dashboard.
- **Caddy** terminates TLS for two separate hostnames and keeps device and browser routes apart.

## What it guarantees

- A device's upload is acknowledged only after it is committed. If the database is down, the device keeps its records and retries.
- Re-sent records are recognised and never stored twice.
- Unregistered devices are never acknowledged, so their records are re-sent once registered, and HR is alerted.
- Raw punch records and the audit log cannot be edited or deleted, even with direct SQL.
- A device user ID belongs to one employee at a time; history is preserved when IDs move.
- Biometric templates and photos are never stored.

## Repository layout

```
backend/
  cmd/api            application API
  cmd/adms           device protocol service
  cmd/migrate        migrations, first admin, dev seed
  cmd/simdevice      SIMULATED device traffic for development
  cmd/healthcheck    container health probe
  internal/protocol  wire-format adapter (pure, unit-tested)
  internal/adms      ingestion service
  internal/api       application endpoints
  internal/auth      passwords, sessions, roles
  internal/db        connection and embedded SQL migrations
frontend/            Next.js dashboard
deploy/              docker-compose.yml, Caddyfile, .env.example, scripts/
docs/                deployment, device, protocol, security, troubleshooting, OpenAPI
```

## Quick start (production)

```bash
cd deploy
cp .env.example .env && chmod 600 .env     # set domains, email, DB password
docker compose up -d --build
./scripts/create-admin.sh you@example.com "Your Name"
```

Full instructions: [`docs/DEPLOYMENT.md`](docs/DEPLOYMENT.md). Then connect the terminal with [`docs/DEVICE_GUIDE.md`](docs/DEVICE_GUIDE.md).

## Documentation

| Document | For |
|---|---|
| [`docs/DEPLOYMENT.md`](docs/DEPLOYMENT.md) | Clean-server install, TLS, backups, monitoring, updates, local development |
| [`docs/DEVICE_GUIDE.md`](docs/DEVICE_GUIDE.md) | Registering and configuring the MB10-VL, mapping user IDs |
| [`docs/PROTOCOL_NOTES.md`](docs/PROTOCOL_NOTES.md) | What is verified, the capture procedure, the Phase 5 acceptance checklist |
| [`docs/SECURITY.md`](docs/SECURITY.md) | Data held, network exposure, device trust, the HTTPS fallback architecture |
| [`docs/TROUBLESHOOTING.md`](docs/TROUBLESHOOTING.md) | Symptom-by-symptom fixes |
| [`docs/openapi.yaml`](docs/openapi.yaml) | Application API reference (OpenAPI 3.1) |
| [`docs/PHASE_REPORT.md`](docs/PHASE_REPORT.md) | Per-phase files, commands, test results, limitations, next steps |
