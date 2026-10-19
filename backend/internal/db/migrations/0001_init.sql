-- 0001_init.sql — core schema for the ADMS application.
-- Conventions:
--   * All instants are timestamptz (stored as UTC).
--   * Device wall-clock times are kept verbatim as `timestamp` (no zone) next to
--     the computed instant, so the original device value is never altered.
--   * Durations are integer minutes. No floating point anywhere.

CREATE EXTENSION IF NOT EXISTS btree_gist;

-- ---------------------------------------------------------------- reference
CREATE TABLE departments (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name        text NOT NULL,
    description text NOT NULL DEFAULT '',
    active      boolean NOT NULL DEFAULT true,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX departments_name_uq ON departments (lower(name));

CREATE TABLE employees (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    employee_number   text NOT NULL UNIQUE,
    first_name        text NOT NULL,
    last_name         text NOT NULL,
    department_id     uuid REFERENCES departments(id),
    position          text NOT NULL DEFAULT '',
    employment_status text NOT NULL DEFAULT 'active'
                      CHECK (employment_status IN ('active','on_leave','inactive','separated')),
    hire_date         date,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX employees_department_idx ON employees (department_id);
CREATE INDEX employees_status_idx ON employees (employment_status);

-- ---------------------------------------------------------------- users / auth
CREATE TABLE users (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name                text NOT NULL,
    email               text NOT NULL,
    password_hash       text,
    external_identity   text,
    role                text NOT NULL CHECK (role IN ('admin','hr','viewer')),
    active              boolean NOT NULL DEFAULT true,
    last_login_at       timestamptz,
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),
    CHECK (password_hash IS NOT NULL OR external_identity IS NOT NULL)
);
CREATE UNIQUE INDEX users_email_uq ON users (lower(email));

CREATE TABLE sessions (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash  bytea NOT NULL UNIQUE,
    source_ip   inet,
    user_agent  text NOT NULL DEFAULT '',
    created_at  timestamptz NOT NULL DEFAULT now(),
    expires_at  timestamptz NOT NULL
);
CREATE INDEX sessions_expires_idx ON sessions (expires_at);

-- ---------------------------------------------------------------- devices
CREATE TABLE biometric_devices (
    id                   uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    device_name          text NOT NULL,
    serial_number        text NOT NULL UNIQUE,
    model                text NOT NULL DEFAULT 'MB10-VL',
    firmware_version     text,
    push_version         text,
    location             text NOT NULL DEFAULT '',
    timezone             text NOT NULL DEFAULT 'Asia/Manila',
    allowed_cidrs        cidr[] NOT NULL DEFAULT '{}',
    enabled              boolean NOT NULL DEFAULT true,
    attlog_stamp         text,
    operlog_stamp        text,
    last_seen_at         timestamptz,
    last_ip              inet,
    last_transaction_at  timestamptz,
    connection_status    text NOT NULL DEFAULT 'never_connected'
                         CHECK (connection_status IN ('never_connected','online','offline','rejected')),
    created_at           timestamptz NOT NULL DEFAULT now(),
    updated_at           timestamptz NOT NULL DEFAULT now(),
    CHECK (serial_number ~ '^[A-Za-z0-9_-]{4,32}$')
);

-- Users enrolled on a device, learned from USERINFO/OPERLOG uploads.
-- Holds only identifiers and display names; biometric templates are never stored.
CREATE TABLE device_users (
    device_id       uuid NOT NULL REFERENCES biometric_devices(id) ON DELETE CASCADE,
    device_user_id  text NOT NULL,
    name            text NOT NULL DEFAULT '',
    privilege       text NOT NULL DEFAULT '',
    first_seen_at   timestamptz NOT NULL DEFAULT now(),
    last_seen_at    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (device_id, device_user_id)
);

CREATE TABLE employee_device_mappings (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    employee_id     uuid NOT NULL REFERENCES employees(id),
    device_id       uuid NOT NULL REFERENCES biometric_devices(id),
    device_user_id  text NOT NULL CHECK (device_user_id ~ '^[A-Za-z0-9]{1,24}$'),
    valid_from      timestamptz NOT NULL DEFAULT now(),
    valid_until     timestamptz,
    created_by      uuid REFERENCES users(id),
    created_at      timestamptz NOT NULL DEFAULT now(),
    CHECK (valid_until IS NULL OR valid_until > valid_from),
    -- One device user ID maps to at most one employee at any instant;
    -- history is preserved by closing (valid_until) rather than deleting.
    EXCLUDE USING gist (
        device_id WITH =,
        device_user_id WITH =,
        tstzrange(valid_from, valid_until, '[)') WITH &&
    )
);
CREATE INDEX edm_employee_idx ON employee_device_mappings (employee_id);

-- ---------------------------------------------------------------- device traffic
CREATE TABLE device_communication_logs (
    id               bigserial PRIMARY KEY,
    device_id        uuid REFERENCES biometric_devices(id) ON DELETE SET NULL,
    serial_number    text,
    method           text NOT NULL,
    path             text NOT NULL,
    query            text NOT NULL DEFAULT '',
    content_type     text NOT NULL DEFAULT '',
    body_size        integer NOT NULL DEFAULT 0,
    body             text NOT NULL DEFAULT '',   -- redacted + truncated copy
    response_status  integer NOT NULL,
    response_body    text NOT NULL DEFAULT '',
    remote_ip        inet,
    outcome          text NOT NULL,
    error            text NOT NULL DEFAULT '',
    duration_ms      integer NOT NULL DEFAULT 0,
    created_at       timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX dcl_device_created_idx ON device_communication_logs (device_id, created_at DESC);
CREATE INDEX dcl_created_idx ON device_communication_logs (created_at DESC);

CREATE TABLE raw_attendance_logs (
    id                     uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    device_id              uuid REFERENCES biometric_devices(id),
    device_serial_number   text NOT NULL,
    device_user_id         text,
    device_transaction_id  text,
    device_timestamp       timestamp,            -- verbatim device wall clock
    punch_at               timestamptz,          -- device_timestamp interpreted in device timezone
    received_at            timestamptz NOT NULL DEFAULT now(),
    event_type             text,                 -- raw status code from device
    verification_method    text,                 -- raw verify code from device
    work_code              text,
    original_payload       text NOT NULL,        -- exact line as received (after line-ending normalisation)
    payload_hash           bytea NOT NULL,
    parse_status           text NOT NULL CHECK (parse_status IN ('accepted','rejected')),
    rejection_reason       text,
    comm_log_id            bigint REFERENCES device_communication_logs(id) ON DELETE SET NULL,
    reprocessed_at         timestamptz,
    UNIQUE (device_serial_number, payload_hash)
);
CREATE INDEX ral_device_user_punch_idx ON raw_attendance_logs (device_id, device_user_id, punch_at);
CREATE INDEX ral_punch_idx ON raw_attendance_logs (punch_at);
CREATE INDEX ral_status_idx ON raw_attendance_logs (parse_status) WHERE parse_status = 'rejected';

-- Raw logs are append-only evidence: block edits to the original fields.
-- A timestamp may only be filled in once, when a previously unparseable line is reprocessed.
CREATE FUNCTION raw_logs_protect() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.original_payload  IS DISTINCT FROM OLD.original_payload
    OR NEW.payload_hash      IS DISTINCT FROM OLD.payload_hash
    OR (OLD.device_timestamp IS NOT NULL AND NEW.device_timestamp IS DISTINCT FROM OLD.device_timestamp)
    OR NEW.device_serial_number IS DISTINCT FROM OLD.device_serial_number
    OR NEW.received_at       IS DISTINCT FROM OLD.received_at THEN
        RAISE EXCEPTION 'raw_attendance_logs original fields are immutable';
    END IF;
    RETURN NEW;
END $$;
CREATE TRIGGER raw_logs_protect BEFORE UPDATE ON raw_attendance_logs
    FOR EACH ROW EXECUTE FUNCTION raw_logs_protect();
CREATE RULE raw_logs_no_delete AS ON DELETE TO raw_attendance_logs DO INSTEAD NOTHING;

CREATE TABLE sync_error_logs (
    id              bigserial PRIMARY KEY,
    device_id       uuid REFERENCES biometric_devices(id) ON DELETE SET NULL,
    serial_number   text,
    error_type      text NOT NULL,
    message         text NOT NULL,
    details         jsonb NOT NULL DEFAULT '{}',
    occurrences     integer NOT NULL DEFAULT 1,
    first_seen_at   timestamptz NOT NULL DEFAULT now(),
    last_seen_at    timestamptz NOT NULL DEFAULT now(),
    resolved_at     timestamptz,
    resolved_by     uuid REFERENCES users(id)
);
-- Collapse repeats of the same open problem into one row with a counter.
CREATE UNIQUE INDEX sel_open_uq ON sync_error_logs (coalesce(serial_number,''), error_type)
    WHERE resolved_at IS NULL;

CREATE TABLE device_commands (
    id            bigserial PRIMARY KEY,
    device_id     uuid NOT NULL REFERENCES biometric_devices(id) ON DELETE CASCADE,
    command       text NOT NULL,
    status        text NOT NULL DEFAULT 'pending'
                  CHECK (status IN ('pending','sent','succeeded','failed','cancelled')),
    return_code   text,
    result        text,
    created_by    uuid REFERENCES users(id),
    created_at    timestamptz NOT NULL DEFAULT now(),
    sent_at       timestamptz,
    completed_at  timestamptz
);
CREATE INDEX dc_pending_idx ON device_commands (device_id, id) WHERE status = 'pending';

-- ---------------------------------------------------------------- scheduling
CREATE TABLE shifts (
    id                    uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name                  text NOT NULL UNIQUE,
    start_time            time NOT NULL,
    end_time              time NOT NULL,
    break_minutes         integer NOT NULL DEFAULT 60 CHECK (break_minutes >= 0),
    break_policy          jsonb NOT NULL DEFAULT '{}',
    grace_period_minutes  integer NOT NULL DEFAULT 0 CHECK (grace_period_minutes >= 0),
    overtime_policy       jsonb NOT NULL DEFAULT '{}',
    overnight             boolean NOT NULL DEFAULT false,
    active                boolean NOT NULL DEFAULT true,
    created_at            timestamptz NOT NULL DEFAULT now(),
    CHECK (overnight = (end_time <= start_time))
);

CREATE TABLE shift_assignments (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    employee_id      uuid NOT NULL REFERENCES employees(id),
    shift_id         uuid NOT NULL REFERENCES shifts(id),
    effective_from   date NOT NULL,
    effective_until  date,
    created_at       timestamptz NOT NULL DEFAULT now(),
    CHECK (effective_until IS NULL OR effective_until >= effective_from),
    EXCLUDE USING gist (
        employee_id WITH =,
        daterange(effective_from, effective_until, '[]') WITH &&
    )
);

CREATE TABLE holidays (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    holiday_date  date NOT NULL,
    name          text NOT NULL,
    holiday_type  text NOT NULL DEFAULT 'regular'
                  CHECK (holiday_type IN ('regular','special_non_working','special_working','company')),
    UNIQUE (holiday_date, name)
);

CREATE TABLE attendance_days (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    employee_id         uuid NOT NULL REFERENCES employees(id),
    attendance_date     date NOT NULL,
    shift_seq           smallint NOT NULL DEFAULT 1,  -- room for multiple shifts per day
    shift_id            uuid REFERENCES shifts(id),
    scheduled_start     timestamptz,
    scheduled_end       timestamptz,
    first_in            timestamptz,
    last_out            timestamptz,
    break_minutes       integer NOT NULL DEFAULT 0,
    worked_minutes      integer NOT NULL DEFAULT 0,
    late_minutes        integer NOT NULL DEFAULT 0,
    undertime_minutes   integer NOT NULL DEFAULT 0,
    overtime_minutes    integer NOT NULL DEFAULT 0,
    attendance_status   text NOT NULL,
    flags               text[] NOT NULL DEFAULT '{}',
    calculation_version integer NOT NULL,
    calculated_at       timestamptz NOT NULL DEFAULT now(),
    UNIQUE (employee_id, attendance_date, shift_seq)
);

CREATE TABLE attendance_adjustments (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    employee_id      uuid NOT NULL REFERENCES employees(id),
    attendance_date  date NOT NULL,
    field_changed    text NOT NULL,
    original_value   text,
    corrected_value  text NOT NULL,
    reason           text NOT NULL,
    status           text NOT NULL DEFAULT 'pending'
                     CHECK (status IN ('pending','approved','rejected')),
    requested_by     uuid NOT NULL REFERENCES users(id),
    approved_by      uuid REFERENCES users(id),
    approved_at      timestamptz,
    created_at       timestamptz NOT NULL DEFAULT now(),
    CHECK (approved_by IS NULL OR approved_by <> requested_by)
);

CREATE TABLE leave_requests (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    employee_id  uuid NOT NULL REFERENCES employees(id),
    leave_type   text NOT NULL,
    start_date   date NOT NULL,
    end_date     date NOT NULL CHECK (end_date >= start_date),
    reason       text NOT NULL DEFAULT '',
    status       text NOT NULL DEFAULT 'pending'
                 CHECK (status IN ('pending','approved','rejected','cancelled')),
    approved_by  uuid REFERENCES users(id),
    created_at   timestamptz NOT NULL DEFAULT now()
);

-- ---------------------------------------------------------------- audit & config
CREATE TABLE audit_logs (
    id             bigserial PRIMARY KEY,
    actor_user_id  uuid REFERENCES users(id),
    action         text NOT NULL,
    entity_type    text NOT NULL,
    entity_id      text,
    before_data    jsonb,
    after_data     jsonb,
    source_ip      inet,
    created_at     timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX audit_entity_idx ON audit_logs (entity_type, entity_id, created_at DESC);
CREATE INDEX audit_created_idx ON audit_logs (created_at DESC);
CREATE RULE audit_no_update AS ON UPDATE TO audit_logs DO INSTEAD NOTHING;
CREATE RULE audit_no_delete AS ON DELETE TO audit_logs DO INSTEAD NOTHING;

CREATE TABLE app_config (
    key         text PRIMARY KEY,
    value       jsonb NOT NULL,
    updated_by  uuid REFERENCES users(id),
    updated_at  timestamptz NOT NULL DEFAULT now()
);
INSERT INTO app_config (key, value) VALUES
    ('business_timezone', '"Asia/Manila"'),
    ('device_online_window_seconds', '300');
