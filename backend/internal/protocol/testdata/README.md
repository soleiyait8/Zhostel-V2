# Device captures

Put payloads captured from the physical MB10-VL here (see docs/PROTOCOL_NOTES.md,
"Capture procedure"), one file per upload, named after firmware and table, e.g.

    mb10vl-fw<version>-attlog-fingerprint.txt
    mb10vl-fw<version>-operlog-users.txt

Then add a test in protocol_test.go that parses each file and asserts the
field values you observed. Replace real names with placeholders before
committing; never commit FP/FACE/BIODATA template lines.

Everything currently in protocol_test.go is SIMULATED.
