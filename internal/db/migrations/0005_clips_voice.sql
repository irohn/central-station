-- Which synthesiser voice produced a TTS clip.
ALTER TABLE clips ADD COLUMN IF NOT EXISTS voice text NOT NULL DEFAULT '';
