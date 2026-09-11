-- Email + password sign-in. Existing (Google) users are verified by definition.
ALTER TABLE users ADD COLUMN IF NOT EXISTS password_hash  text NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN IF NOT EXISTS email_verified boolean NOT NULL DEFAULT false;
ALTER TABLE users ADD COLUMN IF NOT EXISTS verify_token   text NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN IF NOT EXISTS verify_expires timestamptz;
UPDATE users SET email_verified = true WHERE password_hash = '';  -- pre-existing accounts came through an identity provider
CREATE INDEX IF NOT EXISTS users_verify_token_idx ON users (verify_token) WHERE verify_token <> '';
