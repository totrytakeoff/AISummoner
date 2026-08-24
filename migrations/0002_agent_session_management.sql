ALTER TABLE agent_sessions ADD COLUMN archived_at DATETIME NULL;

CREATE INDEX agent_sessions_user_archived_updated_idx
ON agent_sessions(user_id, archived_at, updated_at);

CREATE TABLE agent_user_settings (
    user_id TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    default_approval_mode TEXT NOT NULL CHECK (default_approval_mode IN ('per_command', 'full_access')),
    updated_at DATETIME NOT NULL
);
