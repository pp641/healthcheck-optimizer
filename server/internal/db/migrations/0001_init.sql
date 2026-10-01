-- Tidyfleet initial schema. Devices report health totals only; there is no
-- column anywhere for file names or paths.

CREATE TABLE orgs (
    id                      uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name                    text NOT NULL,
    plan                    text NOT NULL DEFAULT 'pilot',
    enroll_code             text NOT NULL UNIQUE,
    report_interval_minutes int  NOT NULL DEFAULT 60 CHECK (report_interval_minutes BETWEEN 15 AND 1440),
    allow_ai                boolean NOT NULL DEFAULT true,
    slack_webhook_url       text NOT NULL DEFAULT '',
    created_at              timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE users (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id        uuid NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    email         text NOT NULL,
    password_hash text NOT NULL,
    role          text NOT NULL DEFAULT 'admin' CHECK (role IN ('admin', 'viewer')),
    created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX users_email_key ON users (lower(email));

CREATE TABLE sessions (
    token_hash bytea PRIMARY KEY,
    user_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX sessions_expires_idx ON sessions (expires_at);

CREATE TABLE devices (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id           uuid NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    hostname         text NOT NULL,
    os_name          text NOT NULL DEFAULT '',
    os_version       text NOT NULL DEFAULT '',
    agent_version    text NOT NULL DEFAULT '',
    token_hash       bytea NOT NULL UNIQUE,
    enrolled_at      timestamptz NOT NULL DEFAULT now(),
    last_seen        timestamptz,
    last_snapshot_at timestamptz,
    last_metrics     jsonb
);
CREATE INDEX devices_org_idx ON devices (org_id);

-- Snapshots are append-only. (device_id, taken_at) is unique so agents can
-- safely retry a batch. Partition by month once volume calls for it.
CREATE TABLE device_snapshots (
    id          bigserial PRIMARY KEY,
    device_id   uuid NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    taken_at    timestamptz NOT NULL,
    received_at timestamptz NOT NULL DEFAULT now(),
    metrics     jsonb NOT NULL,
    UNIQUE (device_id, taken_at)
);

CREATE TABLE alert_rules (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id     uuid NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    name       text NOT NULL,
    metric     text NOT NULL,
    op         text NOT NULL CHECK (op IN ('gt', 'gte', 'lt', 'lte', 'eq', 'neq')),
    threshold  double precision NOT NULL,
    enabled    boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX alert_rules_org_idx ON alert_rules (org_id);

CREATE TABLE alerts (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id      uuid NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    device_id   uuid NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    rule_id     uuid NOT NULL REFERENCES alert_rules(id) ON DELETE CASCADE,
    status      text NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'resolved')),
    value       double precision,
    message     text NOT NULL,
    opened_at   timestamptz NOT NULL DEFAULT now(),
    resolved_at timestamptz,
    notified_at timestamptz
);
-- At most one open alert per device and rule.
CREATE UNIQUE INDEX alerts_one_open_idx ON alerts (device_id, rule_id) WHERE status = 'open';
CREATE INDEX alerts_org_status_idx ON alerts (org_id, status, opened_at DESC);
