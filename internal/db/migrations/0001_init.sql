CREATE TABLE IF NOT EXISTS users (
    id            uuid PRIMARY KEY,
    email         text NOT NULL UNIQUE,
    name          text NOT NULL,
    picture_url   text NOT NULL DEFAULT '',
    role          text NOT NULL,
    google_sub    text NOT NULL DEFAULT '',
    created_at    timestamptz NOT NULL DEFAULT now(),
    last_login_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS queue_items (
    id            uuid PRIMARY KEY,
    position      bigint NOT NULL,
    track         jsonb NOT NULL,
    added_by_id   text NOT NULL,
    added_by_name text NOT NULL,
    added_at      timestamptz NOT NULL
);
CREATE INDEX IF NOT EXISTS queue_items_position_idx ON queue_items (position);

-- Single-row table holding the logical player state.
CREATE TABLE IF NOT EXISTS playback (
    id         boolean PRIMARY KEY DEFAULT true CHECK (id),
    status     text NOT NULL,
    current    jsonb,
    started_at timestamptz,
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS history (
    id          bigserial PRIMARY KEY,
    item        jsonb NOT NULL,
    started_at  timestamptz NOT NULL,
    ended_at    timestamptz NOT NULL,
    end_reason  text NOT NULL,
    ended_by_id text NOT NULL DEFAULT '',
    error       text NOT NULL DEFAULT ''
);
