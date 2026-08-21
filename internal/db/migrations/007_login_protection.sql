CREATE TABLE IF NOT EXISTS login_attempts (
  id bigserial PRIMARY KEY,
  email text NOT NULL,
  ip inet,
  succeeded boolean NOT NULL DEFAULT false,
  user_agent text,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS login_attempts_email_idx
  ON login_attempts(email, created_at DESC) WHERE NOT succeeded;
CREATE INDEX IF NOT EXISTS login_attempts_ip_idx
  ON login_attempts(ip, created_at DESC) WHERE NOT succeeded;
CREATE INDEX IF NOT EXISTS login_attempts_created_idx
  ON login_attempts(created_at);

CREATE INDEX IF NOT EXISTS sessions_expires_at_idx ON sessions(expires_at);

INSERT INTO settings(key,value,category) VALUES
 ('security.login','{"maxFailures":5,"windowMinutes":15,"lockoutMinutes":15,"maxAddressFailures":25}','security'),
 ('maintenance.retention','{"expiredSessionDays":7,"loginAttemptDays":30}','security')
ON CONFLICT(key) DO NOTHING;
