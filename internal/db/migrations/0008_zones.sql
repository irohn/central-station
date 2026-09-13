-- Zones: each zone is one player node with its own queue, playback state,
-- history, default playlist and volume. Existing data moves to a default zone.
CREATE TABLE IF NOT EXISTS zones (
    id         uuid PRIMARY KEY,
    name       text NOT NULL,
    token_hash text NOT NULL DEFAULT '',
    position   bigint NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS zones_token_hash_idx ON zones (token_hash) WHERE token_hash <> '';

INSERT INTO zones (id, name, position)
VALUES ('00000000-0000-4000-8000-000000000001', 'Office', 1)
ON CONFLICT (id) DO NOTHING;

ALTER TABLE queue_items ADD COLUMN IF NOT EXISTS zone_id uuid NOT NULL
    DEFAULT '00000000-0000-4000-8000-000000000001' REFERENCES zones (id) ON DELETE CASCADE;
ALTER TABLE queue_items ALTER COLUMN zone_id DROP DEFAULT;
CREATE INDEX IF NOT EXISTS queue_items_zone_position_idx ON queue_items (zone_id, position);

ALTER TABLE queue_playlists ADD COLUMN IF NOT EXISTS zone_id uuid NOT NULL
    DEFAULT '00000000-0000-4000-8000-000000000001' REFERENCES zones (id) ON DELETE CASCADE;
ALTER TABLE queue_playlists ALTER COLUMN zone_id DROP DEFAULT;
CREATE INDEX IF NOT EXISTS queue_playlists_zone_position_idx ON queue_playlists (zone_id, position);

ALTER TABLE history ADD COLUMN IF NOT EXISTS zone_id uuid NOT NULL
    DEFAULT '00000000-0000-4000-8000-000000000001' REFERENCES zones (id) ON DELETE CASCADE;
ALTER TABLE history ALTER COLUMN zone_id DROP DEFAULT;
CREATE INDEX IF NOT EXISTS history_zone_id_idx ON history (zone_id, id DESC);

-- playback: one row per zone instead of a single row.
ALTER TABLE playback ADD COLUMN IF NOT EXISTS zone_id uuid;
UPDATE playback SET zone_id = '00000000-0000-4000-8000-000000000001' WHERE zone_id IS NULL;
ALTER TABLE playback DROP CONSTRAINT IF EXISTS playback_pkey;
ALTER TABLE playback DROP COLUMN IF EXISTS id;
ALTER TABLE playback ALTER COLUMN zone_id SET NOT NULL;
ALTER TABLE playback ADD PRIMARY KEY (zone_id);
ALTER TABLE playback ADD CONSTRAINT playback_zone_fk FOREIGN KEY (zone_id) REFERENCES zones (id) ON DELETE CASCADE;

-- Per-zone settings live under "zone:<id>:<key>".
UPDATE settings SET key = 'zone:00000000-0000-4000-8000-000000000001:' || key
WHERE key IN ('fallback', 'volume');

-- Alerts target one zone, or every zone when empty.
ALTER TABLE alerts ADD COLUMN IF NOT EXISTS zone_id text NOT NULL DEFAULT '';
