-- User-queued playlists: play after the song queue, before the default playlist.
CREATE TABLE IF NOT EXISTS queue_playlists (
    id       uuid PRIMARY KEY,
    position bigint NOT NULL,
    data     jsonb NOT NULL
);
CREATE INDEX IF NOT EXISTS queue_playlists_position_idx ON queue_playlists (position);
