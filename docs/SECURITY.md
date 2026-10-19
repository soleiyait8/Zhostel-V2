# Security

## What the system stores

| Data | Where | Notes |
|---|---|---|
| Employee identity (number, name, department, position, hire date) | `employees` | Personal information |
| Device user ID and name as enrolled on the terminal | `device_users`, `employee_device_mappings` | Identifiers only |
| Punch records (user ID, time, status and verify codes, raw line) | `raw_attendance_logs` | Append-only; cannot be edited or deleted |
| Device request log | `device_communication_logs` | Pruned after `ADMS_COMM_LOG_RETENTION_DAYS` (30 by default) |
| User accounts | `users`, `sessions` | Passwords hashed with PBKDF2-SHA256, 600,000 iterations; session tokens stored only as SHA-256 hashes |
| Every change made by a user | `audit_logs` | Append-only |

**Biometric templates are never stored.** Fingerprint, face, palm and photo records that the terminal uploads are acknowledged and discarded, and are redacted from the request log before it is written. Verification happens on the terminal; this server only receives the resulting user ID and time.

Attendance records still identify people. In the Philippines they fall under the Data Privacy Act of 2012 (RA 10173). Have your data protection officer confirm the retention period, the access list and whether registration with the National Privacy Commission applies to your organisation.

## Network exposure

| Port | Exposed | Purpose |
|---|---|---|
| 80, 443 | Yes, Caddy only | HTTPS for the dashboard and the device endpoint; 80 only for certificate issuance and redirects |
| 5432 PostgreSQL | No | On an internal Docker network with no route to the internet |
| 8080 API, 8081 ADMS, 3000 frontend | No | Reachable only through Caddy |
| 4370 ZK pull protocol | No | Not used. Never forward it to a terminal from the internet |

The dashboard and the device endpoint use separate hostnames. The device hostname serves only `/iclock/*`; the dashboard hostname refuses `/iclock/*`. A device credential leak therefore cannot reach the application API, and a browser session cannot reach the device protocol.

## How devices are trusted

The PUSH protocol has no strong device authentication; a device identifies itself only by serial number. The server compensates in layers:

1. **Registration.** Only serials an administrator has registered are acknowledged. Unknown serials get no acknowledgement, so the real device keeps its data, and the attempt appears under **Needs attention**.
2. **Allowed networks.** If the site has a fixed public IP, set it on the device's settings. Requests from anywhere else are refused. Do not rely on this alone where the IP changes.
3. **Bounded impact.** A forged request with a stolen serial can at worst add punch records. These are labelled with the source IP and request log entry, can never overwrite existing records, and are reviewable in Punch history.
4. **Rate limits and size limits** at both Caddy and the ADMS service.

## When the device cannot use HTTPS

Many ZKTeco firmware builds support HTTPS for ADMS; some do not, or handle certificates poorly. Test this first (see `PROTOCOL_NOTES.md`). If HTTPS does not work, do not simply enable the plain-HTTP listener, because punch records would cross the internet unencrypted and the serial number could be captured and replayed. Choose one of the following, in order of preference.

### Option A — on-site relay (recommended)

Place a small always-on machine on the same LAN as the terminal: a Raspberry Pi, a mini PC, or the site's existing server.

```
MB10-VL ──HTTP (LAN only)──▶ relay ──HTTPS (internet)──▶ adms.example.com
```

On the relay, run Caddy with this Caddyfile:

```
:8088 {
	reverse_proxy https://adms.example.com {
		header_up Host adms.example.com
	}
}
```

Point the terminal at the relay's LAN address, port 8088, HTTPS off. Put the relay's public IP in the device's **Allowed source networks**. Unencrypted traffic never leaves the building. Keep the relay's operating system patched.

### Option B — VPN

Connect the site router (or the relay above) to the VPS with WireGuard and point the terminal at the VPS's VPN address. Bind a plain-HTTP device listener to the VPN interface only. This suits organisations that already run site-to-site VPNs.

### Option C — plain HTTP over the internet (not recommended)

Only if A and B are impossible, and only with the risk accepted in writing by the data owner. Uncomment the plain-HTTP block in `deploy/caddy/Caddyfile`, open that one port, and set the device's **Allowed source networks** to the site's fixed IP. Without a fixed IP this option should not be used.

## Hardening checklist

- [ ] `.env` is readable only by root (`chmod 600 deploy/.env`) and not in version control
- [ ] Firewall allows only 22, 80 and 443 inbound (`ufw` commands are in `DEPLOYMENT.md`)
- [ ] SSH uses keys only; password login disabled
- [ ] Each person has their own account; no shared logins
- [ ] Administrators are few; HR staff use the HR role; read-only staff use Viewer
- [ ] Backups run daily and are copied off the server; a restore has been tested
- [ ] Unattended security updates enabled on the host
- [ ] Device allowed networks set wherever the site has a fixed IP
- [ ] `ADMS_ENABLE_COMMANDS` remains `false` until commands are verified

## Application controls

- Every state-changing request needs a valid session and the `X-ADMS-CSRF` header. Browsers cannot add that header cross-site without a CORS preflight, which the API never grants.
- Session cookies are `HttpOnly`, `Secure` and `SameSite=Strict`, last 12 hours by default, and are revoked when a password, role or active flag changes.
- Sign-in is limited to 10 attempts per IP address per 15 minutes; failures are audited.
- All SQL uses bound parameters. Input is validated on the server; the dashboard's checks are only a convenience.
- CSV exports neutralise cells that spreadsheets would treat as formulas.
- Containers run as non-root users, with read-only file systems, all Linux capabilities dropped and `no-new-privileges` set.
- Responses carry HSTS, a Content Security Policy, `X-Frame-Options: DENY` and `nosniff`.
