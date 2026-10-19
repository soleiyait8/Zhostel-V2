# ADMS protocol notes and device verification

This document separates what the adapter implements from what has been proven on a real MB10-VL, and gives the procedure for closing that gap. Until every item in the acceptance checklist passes, the protocol profile stays unverified and the ADMS service logs a warning at every start.

## Status

| Behaviour | Source | Verified on MB10-VL |
|---|---|---|
| Handshake `GET /iclock/cdata?SN=…&options=all` answered with `GET OPTION FROM: <SN>` and option lines | Generic ZKTeco PUSH documentation | No |
| Heartbeat `GET /iclock/getrequest?SN=…` answered with `OK` | Generic PUSH documentation | No |
| Attendance upload `POST /iclock/cdata?SN=…&table=ATTLOG&Stamp=…` | Generic PUSH documentation | No |
| ATTLOG line format `PIN⇥YYYY-MM-DD HH:MM:SS⇥Status⇥Verify⇥WorkCode⇥…` | Generic PUSH documentation | No |
| Upload acknowledgement `OK` (or `OK: <n>`) | Varies by firmware; configurable | No |
| Meaning of Status codes (check-in, check-out, break) | Device configuration dependent | No — stored raw, not interpreted |
| Meaning of Verify codes (fingerprint, face, card, password) | Firmware dependent | No — stored raw, not interpreted |
| User list via `table=OPERLOG` lines beginning `USER PIN=` | Generic PUSH documentation | No |
| Command queue (`C:<id>:<cmd>` on getrequest, results on `/iclock/devicecmd`) | Generic PUSH documentation | No — disabled by default |
| HTTPS from the device | Firmware dependent | No |
| Device clock timezone and `TimeZone=` option | Generic PUSH documentation | No |

Nothing in the business logic depends on an unverified detail except the four acknowledgement and handshake knobs below, which are configuration, not code.

## Tunable settings

All are environment variables of the `adms` service (see `deploy/.env.example`). Change one at a time, restart `adms`, and watch the device's request log.

| Variable | Default | Change it when |
|---|---|---|
| `ADMS_ACK_STYLE` | `ok` | The device keeps resending the same batch after an `OK` reply. Try `ok_count`. |
| `ADMS_LINE_ENDING` | `lf` | The device ignores handshake options (Delay, Realtime). Try `crlf`. |
| `ADMS_TRANS_FLAG` | `1111000000` | The device does not push attendance in real time, or pushes tables you do not need. Newer firmware may expect a named form such as `TransData AttLog	OpLog`. |
| `ADMS_DEVICE_TZ_HOURS` | `8` | Punch times in the database are offset by whole hours. |
| `ADMS_EXTRA_OPTIONS` | empty | The firmware documentation lists further handshake keys, e.g. `ServerVer=2.4.1;PushProtVer=2.4.1`. |
| `ADMS_ENABLE_COMMANDS` | `false` | Only after command responses have been observed and tested. |

## Capture procedure

Do this with the device on the bench, pointed at a staging deployment or at production before go-live.

1. Set `ADMS_LOG_HEARTBEATS=true` in `deploy/.env` and run `docker compose up -d adms` so every exchange is recorded.
2. Register the device on the **Devices** page using the exact serial number from **Menu › System Info › Device Info**. Note the firmware version shown there.
3. Configure the device as described in `DEVICE_GUIDE.md` and restart it.
4. Open the device's page and look at **Recent requests from the device**. Each row shows the method, path, query string, body (biometric template lines are redacted before storage), the reply sent and the outcome.
5. Produce known events and record the exact time you did each:
   - fingerprint punch, user 1, no state key pressed
   - face punch, user 1
   - punch with each state key the site uses (check-in, check-out, break out, break in, overtime in, overtime out)
   - enrol a new test user, then delete them
   - unplug the network cable, punch three times, reconnect
6. Export the raw exchanges for the record:

   ```bash
   docker compose exec -T postgres psql -U adms -d adms -c "\copy (
     SELECT created_at, method, path, query, content_type, body, response_status, response_body, outcome
     FROM device_communication_logs WHERE serial_number='<SERIAL>' ORDER BY id) TO STDOUT WITH CSV HEADER" > capture.csv
   ```

7. Copy representative ATTLOG and OPERLOG bodies (with user IDs replaced if needed) into `backend/internal/protocol/testdata/` and add a test that parses them. This turns the capture into a permanent regression test.
8. Set `ADMS_LOG_HEARTBEATS=false` again afterwards.

## What to look for in the capture

- **Handshake query string.** Record every parameter (`pushver`, `language`, `options`, `PushOptionsFlag` …). If `pushver` is 2.4 or later, the firmware may expect newer option keys; add them through `ADMS_EXTRA_OPTIONS`.
- **Upload bodies.** Confirm fields are tab-separated and the timestamp format is `YYYY-MM-DD HH:MM:SS`. Count the fields per line.
- **Status codes.** Map each state-key punch from step 5 to the code that arrived. Write the mapping into the acceptance record; the Phase 3 attendance engine reads it from configuration.
- **Verify codes.** Map fingerprint and face punches to their codes the same way.
- **Resend behaviour.** After a successful upload, the next request should not repeat the same lines. If it does, the acknowledgement format is wrong.
- **Offline recovery.** After reconnecting, the three offline punches must arrive and appear once each in **Punch history**.
- **Stamps.** Check that the `Stamp` value the device sends increases and that the next handshake reply echoes it in `ATTLOGStamp=`.

## Acceptance checklist (Phase 5)

Mark each item with the date, firmware version and who observed it.

- [ ] Device serial and firmware version recorded
- [ ] Device registers: handshake returns 200 and the device shows as Online
- [ ] Heartbeats arrive at the configured Delay interval
- [ ] A fingerprint punch appears in Punch history within one minute, with the correct local time
- [ ] A face punch appears likewise
- [ ] Status codes mapped for every state key in use
- [ ] Verify codes mapped for fingerprint and face
- [ ] No batch is resent after acknowledgement (no repeated lines in the request log)
- [ ] Three punches made while offline arrive exactly once after reconnection
- [ ] Restarting the `adms` container mid-day loses no punches
- [ ] A device with a wrong serial is refused and appears under Needs attention
- [ ] Transport: HTTPS works from the device, **or** the alternative in `SECURITY.md` is in place
- [ ] Captured payloads committed as test fixtures and `go test ./...` passes
- [ ] `ADMS_PROFILE_VERIFIED=true` set, with the profile name changed to identify the firmware, e.g. `ADMS_PROFILE_NAME=mb10vl-fw-<version>`

When a firmware update is installed on the device, repeat the checklist.
