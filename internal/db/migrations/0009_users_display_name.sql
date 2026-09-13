-- A display name chosen in the app; empty means "use the profile name" (which
-- Google refreshes on every sign-in).
ALTER TABLE users ADD COLUMN IF NOT EXISTS display_name text NOT NULL DEFAULT '';
