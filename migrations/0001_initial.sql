CREATE TABLE users (
    id TEXT PRIMARY KEY,
    username TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    created_at DATETIME NOT NULL
);

CREATE TABLE web_sessions (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_digest BLOB NOT NULL UNIQUE,
    expires_at DATETIME NOT NULL,
    created_at DATETIME NOT NULL
);

CREATE INDEX web_sessions_user_id_idx ON web_sessions(user_id);
CREATE INDEX web_sessions_expires_at_idx ON web_sessions(expires_at);

CREATE TABLE devices (
    id TEXT PRIMARY KEY,
    public_key BLOB NOT NULL UNIQUE,
    owner_user_id TEXT NULL REFERENCES users(id) ON DELETE SET NULL,
    name TEXT NOT NULL,
    platform TEXT NOT NULL,
    arch TEXT NOT NULL,
    client_version TEXT NOT NULL,
    created_at DATETIME NOT NULL,
    paired_at DATETIME NULL,
    last_seen_at DATETIME NULL
);

CREATE INDEX devices_owner_user_id_idx ON devices(owner_user_id);

CREATE TABLE pairing_codes (
    id TEXT PRIMARY KEY,
    device_id TEXT NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    code_digest BLOB NOT NULL UNIQUE,
    expires_at DATETIME NOT NULL,
    consumed_at DATETIME NULL,
    created_at DATETIME NOT NULL
);

CREATE INDEX pairing_codes_device_id_idx ON pairing_codes(device_id);
CREATE INDEX pairing_codes_expires_at_idx ON pairing_codes(expires_at);

CREATE TABLE agent_sessions (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    device_id TEXT NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    approval_mode TEXT NOT NULL CHECK (approval_mode IN ('per_command', 'full_access')),
    provider TEXT NOT NULL,
    external_session_id TEXT NULL UNIQUE,
    state TEXT NOT NULL,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);

CREATE INDEX agent_sessions_user_device_idx ON agent_sessions(user_id, device_id);

CREATE TABLE agent_messages (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL REFERENCES agent_sessions(id) ON DELETE CASCADE,
    role TEXT NOT NULL,
    content TEXT NOT NULL,
    created_at DATETIME NOT NULL
);

CREATE INDEX agent_messages_session_id_idx ON agent_messages(session_id, created_at);

CREATE TABLE tool_calls (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL REFERENCES agent_sessions(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    arguments_json TEXT NOT NULL,
    status TEXT NOT NULL,
    decision TEXT NULL,
    exit_code INTEGER NULL,
    output_excerpt TEXT NULL CHECK (output_excerpt IS NULL OR length(CAST(output_excerpt AS BLOB)) <= 8192),
    created_at DATETIME NOT NULL,
    completed_at DATETIME NULL
);

CREATE INDEX tool_calls_session_id_idx ON tool_calls(session_id, created_at);

CREATE TABLE audit_events (
    id TEXT PRIMARY KEY,
    actor_user_id TEXT NULL REFERENCES users(id) ON DELETE SET NULL,
    device_id TEXT NULL REFERENCES devices(id) ON DELETE SET NULL,
    event_type TEXT NOT NULL,
    metadata_json TEXT NOT NULL,
    created_at DATETIME NOT NULL
);

CREATE INDEX audit_events_created_at_idx ON audit_events(created_at);
CREATE INDEX audit_events_actor_user_id_idx ON audit_events(actor_user_id);
