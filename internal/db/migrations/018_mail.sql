-- Event notifications through the company SMTP relay (internal/mail).
--
-- Installed off. The row names are the ones every service on the network
-- uses (mail.enabled, mail.smtp_host, …) so an operator learns them once.
-- Port 25, no credentials, whatever encryption the relay offers: the shape
-- of an internal relay. mail.password is a secret row — its value column
-- stays empty and the password lives in secret_value, encrypted, which the
-- settings API never returns.
INSERT INTO settings(key,value,secret,category) VALUES
 ('mail.enabled','false',false,'mail'),
 ('mail.smtp_host','""',false,'mail'),
 ('mail.smtp_port','25',false,'mail'),
 ('mail.security','"auto"',false,'mail'),
 ('mail.skip_tls_verify','false',false,'mail'),
 ('mail.username','""',false,'mail'),
 ('mail.password','""',true,'mail'),
 ('mail.from_address','""',false,'mail'),
 ('mail.from_name','"Vendra"',false,'mail'),
 ('mail.base_url','""',false,'mail'),
 ('mail.timeout_seconds','10',false,'mail'),
 ('mail.notify_approval_request','true',false,'mail'),
 ('mail.notify_approval_decision','true',false,'mail'),
 ('mail.notify_expiry','true',false,'mail'),
 ('mail.notify_alert','true',false,'mail')
ON CONFLICT(key) DO NOTHING;

-- Every attempt, sent or not. Subject and recipient only — no body, so the
-- log cannot become the way a contract's terms leave the building.
CREATE TABLE IF NOT EXISTS mail_deliveries (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  event text NOT NULL,
  recipient text NOT NULL,
  subject text NOT NULL,
  object_type text,
  object_id uuid,
  actor_id uuid,
  status text NOT NULL DEFAULT 'queued' CHECK (status IN ('queued','sent','failed')),
  attempts integer NOT NULL DEFAULT 0,
  error_message text,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_mail_deliveries_created ON mail_deliveries(created_at DESC);
