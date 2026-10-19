# Troubleshooting

Start with the device's page in the dashboard: its status, **Open problems**, **Traffic, last 24 hours** and **Recent requests from the device** answer most questions. Then use the container logs:

```bash
cd /opt/adms/deploy
docker compose ps
docker compose logs --since 30m adms
docker compose logs --since 30m caddy
```

## The device shows "Not connected yet"

No request carrying this serial has reached the server.

1. **Is the request reaching Caddy?** `docker compose logs caddy | grep iclock`. If nothing appears, the problem is between the terminal and the server.
2. **Check the terminal's network.** Gateway, DNS and internet access. A terminal that cannot resolve DNS will not connect by domain name; try the server's IP with "Enable domain name" off, as a test only.
3. **Check the server address and port** on the terminal against **Connecting the terminal** on the device page.
4. **HTTPS.** If requests never arrive with HTTPS on, the firmware may not support it or may reject the certificate. See `SECURITY.md`.
5. **Firewall.** From another network: `curl -I https://adms.example.com/iclock/cdata` should return `400`.

## "Unregistered device is trying to connect"

A request arrived with a serial that is not registered. The alert shows the serial and source IP.

- If it is your terminal and the serial differs from what you registered (often a typo or letter O versus digit 0), register the serial shown in the alert.
- If you do not recognise it, leave it. Unregistered devices are never acknowledged and cannot add data.

## "Device connected from an unexpected network"

The site's public IP is not in the device's **Allowed source networks**. Many internet connections change IP occasionally. Update the list, or clear it if the site has no fixed IP.

## Online, but punches do not appear

1. In **Recent requests from the device**, look for `POST /iclock/cdata` with `table=ATTLOG`.
   - **None:** the device is not pushing attendance. Check `Realtime` and `TransFlag` in `PROTOCOL_NOTES.md`, and that the terminal's push or real-time upload option is enabled.
   - **Present, result 503:** the database was unavailable. The device keeps the records and retries automatically. Check `docker compose ps postgres`.
   - **Present, result 200:** the records were stored. Check **Punch history** with the right date range and **Records: All**.
2. Punches stored but under a different day: the device's clock or timezone setting is wrong. Compare the **device:** time in Punch history with the received time.

## The same punches keep arriving again

The device is not recognising the acknowledgement. Duplicates are never stored twice, so data is safe, but the device is wasting effort. Try `ADMS_ACK_STYLE=ok_count`, then `ADMS_LINE_ENDING=crlf`, restarting `adms` after each change (`docker compose up -d adms`). Record which one works in the acceptance checklist.

## Records "held for review"

Lines that could not be read safely are kept, never discarded. **Punch history › Records: Held for review** shows each one with its reason and original text.

| Reason | Usual cause | Action |
|---|---|---|
| Punch time is in the future | Device clock was wrong | Fix the clock, then **Accept them after fixing the device clock** on the device page |
| Line is not in the expected tab-separated format | Different firmware format | Capture and report; see `PROTOCOL_NOTES.md` |
| Date or time could not be read | Different firmware format, or corruption | Same |

After a software update that changes parsing, **Re-check held records** on the device page re-evaluates them.

## Punches show as "Unmapped ID"

The device user ID is not linked to an employee for that moment. Map it from the device page. Mappings are not applied backwards in time, so earlier punches stay unmapped; this protects history when IDs are reused.

## Cannot sign in

- "Too many sign-in attempts": wait 15 minutes.
- Lost the only administrator password: create another administrator with `./scripts/create-admin.sh`.
- Signed out immediately after signing in: the dashboard must be served over HTTPS because session cookies are marked Secure.

## Certificates are not issued

`docker compose logs caddy | grep -i acme`. Usual causes: DNS does not point at this server yet, port 80 is blocked, or a rate limit after many failed attempts. Fix the cause and run `docker compose restart caddy`.

## A container is unhealthy

```bash
docker compose ps
docker compose logs --tail 100 <service>
```

`api` and `adms` report unhealthy when they cannot reach the database. `migrate` must have exited with code 0; if it failed, its log shows which migration and why.
