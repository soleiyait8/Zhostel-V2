# Deployment

These steps take a fresh Ubuntu 24.04 VPS to a running system. Allow about 30 minutes. Commands run as root unless shown otherwise.

## 1. Requirements

- A VPS with 2 vCPU, 2 GB RAM and 20 GB disk handles several devices and a few hundred employees comfortably.
- Two DNS names pointing at the VPS's public IP (A record, plus AAAA if it has IPv6):
  - `attendance.example.com` for the HR dashboard
  - `adms.example.com` for the terminals
- Ports 80 and 443 reachable from the internet. Caddy needs both to obtain certificates.

Confirm DNS before continuing:

```bash
dig +short attendance.example.com
dig +short adms.example.com
```

Both must return the VPS's IP.

## 2. Prepare the host

```bash
apt update && apt -y upgrade
apt -y install ca-certificates curl git ufw unattended-upgrades
dpkg-reconfigure -f noninteractive unattended-upgrades

ufw default deny incoming
ufw default allow outgoing
ufw allow OpenSSH
ufw allow 80/tcp
ufw allow 443/tcp
ufw allow 443/udp
ufw enable
```

Install Docker Engine with the Compose plugin from Docker's repository:

```bash
install -m 0755 -d /etc/apt/keyrings
curl -fsSL https://download.docker.com/linux/ubuntu/gpg -o /etc/apt/keyrings/docker.asc
echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/ubuntu $(. /etc/os-release && echo $VERSION_CODENAME) stable" > /etc/apt/sources.list.d/docker.list
apt update && apt -y install docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
docker compose version
```

Docker publishes container ports by editing iptables directly, bypassing `ufw`. That is why only Caddy publishes ports in this stack. Do not add `ports:` to any other service.

## 3. Get the code and configure

```bash
mkdir -p /opt && cd /opt
# copy or clone the project here so that /opt/adms/deploy exists
cd /opt/adms/deploy
cp .env.example .env
chmod 600 .env
sed -i "s/REPLACE_WITH_openssl_rand_hex_32/$(openssl rand -hex 32)/" .env
nano .env        # set APP_DOMAIN, ADMS_DOMAIN, ACME_EMAIL
```

## 4. Start

```bash
cd /opt/adms/deploy
docker compose up -d --build
docker compose ps
```

The first build takes a few minutes. `migrate` runs once and exits with code 0; every other service should become `healthy`. Caddy requests certificates on first start; watch it with:

```bash
docker compose logs -f caddy
```

## 5. Create the first administrator

```bash
./scripts/create-admin.sh you@example.com "Your Name"
```

Open `https://attendance.example.com`, sign in, then:

1. Add the other user accounts under **Users**.
2. Create departments, then add or import employees under **Employees**.
3. Register the terminal under **Devices**, then configure it with `DEVICE_GUIDE.md`.

## 6. Prove the device path before touching the terminal (optional)

Register a device with serial `SIMTEST0001`, then:

```bash
./scripts/simulate-device.sh SIMTEST0001
```

The output must show a handshake reply starting `GET OPTION FROM`, `OK` for the heartbeat and both uploads. **Punch history** will show simulated punches for user IDs 1001–1004 and 9999, plus one held record. Disable or delete the simulated device afterwards; its records remain labelled with its serial.

## 7. Backups

```bash
mkdir -p /opt/adms/deploy/backups
crontab -e
# add:
15 2 * * *  cd /opt/adms/deploy && ./scripts/backup.sh >> backups/backup.log 2>&1
```

Each dump is checked with `pg_restore --list` before it is kept, and a SHA-256 checksum is written beside it. **Copy the backups directory off the server** (for example with `rclone` to object storage, or `rsync` to another host); a backup on the same disk does not survive the loss of that disk.

To restore:

```bash
./scripts/restore.sh backups/adms-20261019T021500Z.dump --yes
```

The restore runs as a single transaction: it either fully succeeds or leaves the database untouched. Terminals hold their records while the ADMS service is stopped and resend them afterwards.

Test a restore on a separate machine at least once before relying on it.

## 8. Monitoring

- **Health endpoints.** Each container has a health check; `docker compose ps` shows the state. For external uptime monitoring, check `https://attendance.example.com/login` (expects 200) and `https://adms.example.com/iclock/cdata` (expects 400 — reachable and validating input).
- **Logs** are structured JSON on stdout, rotated at 20 MB × 5 files per container: `docker compose logs --since 1h adms`.
- **Device silence** is the most important signal. The dashboard marks a device Offline when it has not been heard from for 5 minutes (`device_online_window_seconds` in the `app_config` table). For alerts outside working hours, point an external monitor at a query of `last_seen_at`, or add a check to your existing monitoring.

## 9. Updating

```bash
cd /opt/adms/deploy
./scripts/backup.sh
git pull                       # or copy in the new release
docker compose up -d --build
docker compose ps
```

Migrations run automatically through the `migrate` service before the API and ADMS service start.

## 10. Running locally for development

Requirements: Go 1.24, Node 22, PostgreSQL 16 with the `btree_gist` extension available.

```bash
createdb adms_dev
export DATABASE_URL="postgres://localhost/adms_dev?sslmode=disable"
export APP_ENV=development COOKIE_SECURE=false TRUST_PROXY=false ADMIN_PASSWORD="choose a long password"
cd backend
go run ./cmd/migrate -create-admin you@example.com -seed-dev
go run ./cmd/adms &          # :8081
go run ./cmd/api &           # :8080
go run ./cmd/simdevice       # SIMULATED traffic to :8081

cd ../frontend && npm ci && npm run dev    # http://localhost:3000
```

Tests:

```bash
cd backend
go test ./...                                   # unit tests only
ADMS_TEST_DATABASE_URL="postgres://localhost/adms_test?sslmode=disable" go test -p 1 ./...   # with integration tests
```

The integration tests drop and recreate the `public` schema of the test database. Point them only at a database you can lose.
