-- Short audio played outside the music queue: recordings, TTS output, chimes.
CREATE TABLE IF NOT EXISTS clips (
    id               uuid PRIMARY KEY,
    name             text NOT NULL,
    kind             text NOT NULL,
    mime             text NOT NULL,
    size             integer NOT NULL,
    duration_seconds double precision NOT NULL DEFAULT 0,
    text             text NOT NULL DEFAULT '',
    created_by_id    text NOT NULL DEFAULT '',
    created_by_name  text NOT NULL DEFAULT '',
    created_at       timestamptz NOT NULL DEFAULT now(),
    builtin          boolean NOT NULL DEFAULT false,
    audio            bytea NOT NULL
);
