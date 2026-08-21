CREATE TABLE IF NOT EXISTS saved_views (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  organization_id uuid REFERENCES organizations(id) ON DELETE CASCADE,
  context text NOT NULL,
  name text NOT NULL,
  filters jsonb NOT NULL DEFAULT '{}',
  columns jsonb NOT NULL DEFAULT '[]',
  shared boolean NOT NULL DEFAULT false,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(user_id, context, name)
);
CREATE INDEX IF NOT EXISTS saved_views_context_idx
  ON saved_views(context, organization_id, updated_at DESC);

CREATE TABLE IF NOT EXISTS user_form_drafts (
  user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  draft_key text NOT NULL,
  payload jsonb NOT NULL DEFAULT '{}',
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY(user_id, draft_key)
);

CREATE TABLE IF NOT EXISTS user_work_item_states (
  user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  item_key text NOT NULL,
  state text NOT NULL DEFAULT 'active' CHECK(state IN ('active','done','snoozed')),
  snoozed_until timestamptz,
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY(user_id, item_key)
);
CREATE INDEX IF NOT EXISTS user_work_item_states_snooze_idx
  ON user_work_item_states(user_id, state, snoozed_until);
