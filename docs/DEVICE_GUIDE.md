# Connecting the MB10-VL

Menu names below follow common ZKTeco firmware. Your unit's wording can differ slightly; the device's user manual for its installed firmware is the authority.

## Before you start

- The server is deployed and you can sign in as an administrator.
- You know the device hostname from `deploy/.env` (`ADMS_DOMAIN`, for example `adms.example.com`).
- The terminal has a working network connection (Ethernet or Wi-Fi) with internet access. Check with the network test under **COMM.** if your firmware has one.
- The terminal's date and time are correct. Punch times are recorded exactly as the device's clock says.

## 1. Read the serial number and firmware

On the terminal: **Menu › System Info › Device Info**. Write down the **Serial Number** exactly, and the **Firmware Version**.

## 2. Register the device

In the dashboard: **Devices › Register device**.

| Field | What to enter |
|---|---|
| Name | Something HR will recognise, e.g. "Main lobby" |
| Serial number | Exactly as shown on the device |
| Location | Optional |
| Device clock timezone | `Asia/Manila` unless the terminal's clock is set to another zone |
| Allowed source networks | The site's fixed public IP if it has one; otherwise leave empty |

Register the device **before** pointing it at the server. If it connects first, it is refused (its records stay on the device) and listed under **Needs attention**. Registering it clears that alert, and the device then sends everything it holds.

## 3. Point the device at the server

On the terminal: **Menu › COMM. › Cloud Server Setting** (sometimes **ADMS** or **Cloud Server**).

| Setting | Value |
|---|---|
| Server mode | ADMS |
| Enable domain name | On |
| Server address | your `ADMS_DOMAIN`, e.g. `adms.example.com` |
| Server port | 443 |
| HTTPS | On |
| Enable proxy server | Off |

The device page in the dashboard shows these same values under **Connecting the terminal**.

If the firmware has no HTTPS option, or HTTPS fails to connect, stop here and choose an option in `SECURITY.md` › *When the device cannot use HTTPS*. With the recommended on-site relay, the server address is the relay's LAN IP, port 8088, HTTPS off.

Save and restart the terminal.

## 4. Confirm it connected

Within a minute the device page should show:

- **Status:** Online
- **Last contact:** just now, with the site's public IP
- **Firmware:** filled in, if the firmware reports it
- In **Recent requests from the device**, a `GET /iclock/cdata` handshake with result 200

If not, see `TROUBLESHOOTING.md`.

## 5. Make a test punch

Punch on the terminal with an enrolled user. Within a minute it should appear in **Punch history** with the correct local time. If the user ID is not yet linked to an employee, it shows as **Unmapped ID**.

## 6. Link device user IDs to employees

Every person enrolled on the terminal has a user ID (the number shown under **User Mgt** on the device). The server needs to know which employee each ID belongs to.

- **From the device page:** **User IDs on this device** lists every ID the server has seen, with the name stored on the terminal. Choose **Map to employee** beside each unmapped ID.
- **From an employee's page:** **Device user IDs › Map ID**.

Mappings apply from the moment they are created. To move an ID to a different person, end the old mapping first; past punches stay with the original employee. Deactivating an employee ends their mappings automatically.

Punches recorded before an ID was mapped remain **Unmapped**. They are kept and listed on the dashboard so HR can resolve them; the attendance engine (Phase 3) will provide the correction workflow for those days.

## 7. Before go-live

Work through the acceptance checklist in `PROTOCOL_NOTES.md`. It confirms, on your exact firmware, that punches are not duplicated or lost across a network outage, and records what the device's status and verify codes mean.

## Moving or replacing a terminal

- **Moving a terminal:** update its location and, if needed, its allowed networks on the device page. Nothing else changes.
- **Replacing a terminal:** register the new one by its own serial and map its user IDs. Disable the old one on its device page; its history stays.
- **Clock changes:** if a terminal's clock was wrong, punches dated more than a day in the future are held for review. After fixing the clock, an administrator can accept them from the device page.
