PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS system_settings (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS admin_credentials (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    password_hash TEXT NOT NULL,
    default_password_active INTEGER NOT NULL DEFAULT 1,
    updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS rooms (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    room_code TEXT NOT NULL UNIQUE,
    room_name TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL,
    is_permanent INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL,
    last_active_at INTEGER NOT NULL,
    closed_at INTEGER,
    delete_after INTEGER
);

CREATE INDEX IF NOT EXISTS idx_rooms_status_last_active ON rooms(status, last_active_at);
CREATE INDEX IF NOT EXISTS idx_rooms_delete_after ON rooms(delete_after);

CREATE TABLE IF NOT EXISTS messages (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    room_id INTEGER NOT NULL,
    type TEXT NOT NULL,
    text_content TEXT,
    file_name TEXT,
    file_path TEXT,
    file_size INTEGER,
    mime_type TEXT,
    is_pinned INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL,
    expires_at INTEGER,
    FOREIGN KEY(room_id) REFERENCES rooms(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_messages_room_id_id ON messages(room_id, id);
CREATE INDEX IF NOT EXISTS idx_messages_expires_at ON messages(expires_at);

CREATE TABLE IF NOT EXISTS room_events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    room_id INTEGER NOT NULL,
    event_type TEXT NOT NULL,
    message_id INTEGER,
    created_at INTEGER NOT NULL,
    FOREIGN KEY(room_id) REFERENCES rooms(id) ON DELETE CASCADE,
    FOREIGN KEY(message_id) REFERENCES messages(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS upload_sessions (
    id TEXT PRIMARY KEY,
    room_code TEXT NOT NULL,
    file_name TEXT NOT NULL,
    file_size INTEGER NOT NULL,
    mime_type TEXT NOT NULL,
    chunk_size INTEGER NOT NULL,
    uploaded_size INTEGER NOT NULL,
    stored_path TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_room_events_room_id_id ON room_events(room_id, id);
CREATE INDEX IF NOT EXISTS idx_upload_sessions_room_code ON upload_sessions(room_code);

CREATE TABLE IF NOT EXISTS hourly_stats (
    hour TEXT PRIMARY KEY,
    room_count INTEGER NOT NULL DEFAULT 0,
    message_count INTEGER NOT NULL DEFAULT 0,
    updated_at INTEGER NOT NULL
);

ALTER TABLE rooms ADD COLUMN room_name TEXT NOT NULL DEFAULT '';
