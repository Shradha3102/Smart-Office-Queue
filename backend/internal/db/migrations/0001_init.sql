-- Smart Office Queue & Token Management System
-- Initial schema

CREATE TABLE departments (
    id                   SERIAL PRIMARY KEY,
    code                 VARCHAR(10)  NOT NULL UNIQUE,
    name                 VARCHAR(80)  NOT NULL,
    description          TEXT         NOT NULL DEFAULT '',
    -- Fallback service time used for ETA until enough real data exists.
    avg_service_minutes  INT          NOT NULL DEFAULT 5 CHECK (avg_service_minutes BETWEEN 1 AND 240),
    is_paused            BOOLEAN      NOT NULL DEFAULT FALSE,
    pause_reason         TEXT         NOT NULL DEFAULT '',
    paused_at            TIMESTAMPTZ,
    sort_order           INT          NOT NULL DEFAULT 0,
    created_at           TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at           TIMESTAMPTZ  NOT NULL DEFAULT now()
);

CREATE TABLE staff_users (
    id             SERIAL PRIMARY KEY,
    name           VARCHAR(80)  NOT NULL,
    email          VARCHAR(160) NOT NULL,
    password_hash  TEXT         NOT NULL,
    role           VARCHAR(10)  NOT NULL CHECK (role IN ('admin', 'staff')),
    department_id  INT REFERENCES departments(id) ON DELETE RESTRICT,
    is_active      BOOLEAN      NOT NULL DEFAULT TRUE,
    created_at     TIMESTAMPTZ  NOT NULL DEFAULT now(),
    CONSTRAINT staff_requires_department CHECK (role = 'admin' OR department_id IS NOT NULL)
);
CREATE UNIQUE INDEX staff_users_email_uq ON staff_users (lower(email));

-- Per-department, per-day running counter used to mint IT-001, IT-002 ...
CREATE TABLE token_counters (
    department_id  INT  NOT NULL REFERENCES departments(id) ON DELETE CASCADE,
    business_date  DATE NOT NULL,
    last_seq       INT  NOT NULL,
    PRIMARY KEY (department_id, business_date)
);

-- Global monotonically increasing ordering key. A token's place in its lane is
-- its queue_seq; "move to end of queue" == take a fresh value.
CREATE SEQUENCE token_queue_seq;

CREATE TABLE tokens (
    id                  UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    token_number        VARCHAR(20)  NOT NULL,
    department_id       INT          NOT NULL REFERENCES departments(id) ON DELETE RESTRICT,
    business_date       DATE         NOT NULL,
    seq                 INT          NOT NULL,
    visitor_name        VARCHAR(80)  NOT NULL,
    visitor_phone       VARCHAR(20)  NOT NULL DEFAULT '',
    is_priority         BOOLEAN      NOT NULL DEFAULT FALSE,
    priority_reason     VARCHAR(100) NOT NULL DEFAULT '',
    -- 1 = priority lane, 0 = normal lane. Waiting order is (priority_lane DESC, queue_seq ASC).
    priority_lane       SMALLINT     NOT NULL DEFAULT 0 CHECK (priority_lane IN (0, 1)),
    queue_seq           BIGINT       NOT NULL,
    status              VARCHAR(12)  NOT NULL CHECK (status IN ('waiting', 'serving', 'completed', 'cancelled', 'no_show')),
    no_show_count       SMALLINT     NOT NULL DEFAULT 0,
    transfer_count      SMALLINT     NOT NULL DEFAULT 0,
    transferred_from    VARCHAR(20)  NOT NULL DEFAULT '',
    access_secret_hash  VARCHAR(64)  NOT NULL,
    source              VARCHAR(10)  NOT NULL DEFAULT 'visitor' CHECK (source IN ('visitor', 'staff')),
    served_by           INT REFERENCES staff_users(id) ON DELETE SET NULL,
    close_reason        TEXT         NOT NULL DEFAULT '',
    created_at          TIMESTAMPTZ  NOT NULL DEFAULT now(),
    enqueued_at         TIMESTAMPTZ  NOT NULL DEFAULT now(),
    first_called_at     TIMESTAMPTZ,
    called_at           TIMESTAMPTZ,
    completed_at        TIMESTAMPTZ,
    closed_at           TIMESTAMPTZ,
    updated_at          TIMESTAMPTZ  NOT NULL DEFAULT now(),
    CONSTRAINT tokens_dept_day_seq_uq UNIQUE (department_id, business_date, seq)
);

-- Hard guarantee: at most one token being served per department.
CREATE UNIQUE INDEX tokens_one_serving_per_dept ON tokens (department_id) WHERE status = 'serving';
-- Fast queue scans.
CREATE INDEX tokens_waiting_order_idx ON tokens (department_id, priority_lane DESC, queue_seq) WHERE status = 'waiting';
CREATE INDEX tokens_day_idx ON tokens (business_date, department_id, status);
CREATE INDEX tokens_phone_active_idx ON tokens (department_id, visitor_phone) WHERE status IN ('waiting', 'serving');

-- Append-only audit trail; also the source for dashboard metrics.
CREATE TABLE token_events (
    id             BIGSERIAL    PRIMARY KEY,
    token_id       UUID REFERENCES tokens(id) ON DELETE CASCADE,
    department_id  INT  NOT NULL REFERENCES departments(id) ON DELETE CASCADE,
    event_type     VARCHAR(24)  NOT NULL,
    actor_id       INT REFERENCES staff_users(id) ON DELETE SET NULL,
    details        JSONB        NOT NULL DEFAULT '{}'::jsonb,
    created_at     TIMESTAMPTZ  NOT NULL DEFAULT now()
);
CREATE INDEX token_events_dept_time_idx ON token_events (department_id, created_at);
CREATE INDEX token_events_token_idx ON token_events (token_id, created_at);
