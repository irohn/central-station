-- Scheduled announcements.
CREATE TABLE IF NOT EXISTS alerts (
    id              uuid PRIMARY KEY,
    name            text NOT NULL,
    enabled         boolean NOT NULL DEFAULT true,
    type            text NOT NULL DEFAULT 'cron',
    schedule        text NOT NULL,
    timezone        text NOT NULL,
    clip_id         uuid NOT NULL,
    loops           integer NOT NULL DEFAULT 1,
    chime_id        text NOT NULL DEFAULT '',
    created_by_id   text NOT NULL DEFAULT '',
    created_by_name text NOT NULL DEFAULT '',
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    last_fired_at   timestamptz,
    last_error      text NOT NULL DEFAULT ''
);
