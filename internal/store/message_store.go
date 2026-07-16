package store

import (
	"database/sql"
	"time"
)

type Message struct {
	ID          int64  `json:"id"`
	RoomID      int64  `json:"roomId"`
	Type        string `json:"type"`
	TextContent string `json:"textContent"`
	FileName    string `json:"fileName,omitempty"`
	FilePath    string `json:"filePath,omitempty"`
	FileSize    int64  `json:"fileSize,omitempty"`
	MimeType    string `json:"mimeType,omitempty"`
	IsPinned    bool   `json:"isPinned"`
	CreatedAt   int64  `json:"createdAt"`
	ExpiresAt   *int64 `json:"expiresAt,omitempty"`
}

type RoomEvent struct {
	ID        int64  `json:"id"`
	RoomID    int64  `json:"roomId"`
	EventType string `json:"eventType"`
	MessageID *int64 `json:"messageId,omitempty"`
	CreatedAt int64  `json:"createdAt"`
}

type UploadSession struct {
	ID           string `json:"id"`
	RoomCode     string `json:"roomCode"`
	FileName     string `json:"fileName"`
	FileSize     int64  `json:"fileSize"`
	MimeType     string `json:"mimeType"`
	ChunkSize    int64  `json:"chunkSize"`
	UploadedSize int64  `json:"uploadedSize"`
	StoredPath   string `json:"storedPath"`
	CreatedAt    int64  `json:"createdAt"`
	UpdatedAt    int64  `json:"updatedAt"`
}

type UploadSessionStore struct {
	db *sql.DB
}

func NewUploadSessionStore(db *sql.DB) *UploadSessionStore {
	return &UploadSessionStore{db: db}
}

func (s *UploadSessionStore) Create(session UploadSession) error {
	_, err := s.db.Exec(`INSERT INTO upload_sessions(id, room_code, file_name, file_size, mime_type, chunk_size, uploaded_size, stored_path, created_at, updated_at) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, session.ID, session.RoomCode, session.FileName, session.FileSize, session.MimeType, session.ChunkSize, session.UploadedSize, session.StoredPath, session.CreatedAt, session.UpdatedAt)
	return err
}

func (s *UploadSessionStore) Get(id string) (UploadSession, error) {
	row := s.db.QueryRow(`SELECT id, room_code, file_name, file_size, mime_type, chunk_size, uploaded_size, stored_path, created_at, updated_at FROM upload_sessions WHERE id = ?`, id)
	var session UploadSession
	if err := row.Scan(&session.ID, &session.RoomCode, &session.FileName, &session.FileSize, &session.MimeType, &session.ChunkSize, &session.UploadedSize, &session.StoredPath, &session.CreatedAt, &session.UpdatedAt); err != nil {
		return UploadSession{}, err
	}
	return session, nil
}

func (s *UploadSessionStore) ListByRoomCode(roomCode string) ([]UploadSession, error) {
	rows, err := s.db.Query(`SELECT id, room_code, file_name, file_size, mime_type, chunk_size, uploaded_size, stored_path, created_at, updated_at FROM upload_sessions WHERE room_code = ? ORDER BY created_at ASC`, roomCode)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []UploadSession
	for rows.Next() {
		var session UploadSession
		if err := rows.Scan(&session.ID, &session.RoomCode, &session.FileName, &session.FileSize, &session.MimeType, &session.ChunkSize, &session.UploadedSize, &session.StoredPath, &session.CreatedAt, &session.UpdatedAt); err != nil {
			return nil, err
		}
		sessions = append(sessions, session)
	}
	return sessions, rows.Err()
}

func (s *UploadSessionStore) UpdateProgress(id string, uploadedSize int64, updatedAt time.Time) error {
	_, err := s.db.Exec(`UPDATE upload_sessions SET uploaded_size = ?, updated_at = ? WHERE id = ?`, uploadedSize, updatedAt.Unix(), id)
	return err
}

func (s *UploadSessionStore) Delete(id string) error {
	_, err := s.db.Exec(`DELETE FROM upload_sessions WHERE id = ?`, id)
	return err
}

func (s *UploadSessionStore) DeleteByRoomCode(roomCode string) error {
	_, err := s.db.Exec(`DELETE FROM upload_sessions WHERE room_code = ?`, roomCode)
	return err
}

type MessageStore struct {
	db *sql.DB
}

func NewMessageStore(db *sql.DB) *MessageStore {
	return &MessageStore{db: db}
}

func (s *MessageStore) Create(roomID int64, text string, now time.Time) (Message, error) {
	result, err := s.db.Exec(`INSERT INTO messages(room_id, type, text_content, created_at) VALUES(?, 'text', ?, ?)`, roomID, text, now.Unix())
	if err != nil {
		return Message{}, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return Message{}, err
	}
	return s.Get(id)
}

func (s *MessageStore) CreateFile(roomID int64, fileName string, filePath string, fileSize int64, mimeType string, expiresAt *time.Time, now time.Time) (Message, error) {
	var expiresAtUnix *int64
	if expiresAt != nil {
		v := expiresAt.Unix()
		expiresAtUnix = &v
	}
	result, err := s.db.Exec(`INSERT INTO messages(room_id, type, file_name, file_path, file_size, mime_type, created_at, expires_at) VALUES(?, 'file', ?, ?, ?, ?, ?, ?)`, roomID, fileName, filePath, fileSize, mimeType, now.Unix(), expiresAtUnix)
	if err != nil {
		return Message{}, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return Message{}, err
	}
	return s.Get(id)
}

func (s *MessageStore) Get(id int64) (Message, error) {
	row := s.db.QueryRow(`SELECT id, room_id, type, COALESCE(text_content, ''), COALESCE(file_name, ''), COALESCE(file_path, ''), COALESCE(file_size, 0), COALESCE(mime_type, ''), is_pinned, created_at, expires_at FROM messages WHERE id = ?`, id)
	return scanMessage(row)
}

func (s *MessageStore) ListByRoom(roomID int64) ([]Message, error) {
	rows, err := s.db.Query(`SELECT id, room_id, type, COALESCE(text_content, ''), COALESCE(file_name, ''), COALESCE(file_path, ''), COALESCE(file_size, 0), COALESCE(mime_type, ''), is_pinned, created_at, expires_at FROM messages WHERE room_id = ? ORDER BY is_pinned DESC, created_at DESC, id DESC`, roomID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []Message
	for rows.Next() {
		message, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		messages = append(messages, message)
	}
	return messages, rows.Err()
}

func (s *MessageStore) AppendEvent(roomID int64, messageID int64, now time.Time) (RoomEvent, error) {
	result, err := s.db.Exec(`INSERT INTO room_events(room_id, event_type, message_id, created_at) VALUES(?, 'message_created', ?, ?)`, roomID, messageID, now.Unix())
	if err != nil {
		return RoomEvent{}, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return RoomEvent{}, err
	}
	row := s.db.QueryRow(`SELECT id, room_id, event_type, message_id, created_at FROM room_events WHERE id = ?`, id)
	return scanEvent(row)
}

func (s *MessageStore) AppendFileEvent(roomID int64, messageID int64, now time.Time) (RoomEvent, error) {
	result, err := s.db.Exec(`INSERT INTO room_events(room_id, event_type, message_id, created_at) VALUES(?, 'file_created', ?, ?)`, roomID, messageID, now.Unix())
	if err != nil {
		return RoomEvent{}, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return RoomEvent{}, err
	}
	row := s.db.QueryRow(`SELECT id, room_id, event_type, message_id, created_at FROM room_events WHERE id = ?`, id)
	return scanEvent(row)
}

func (s *MessageStore) AppendPinEvent(roomID int64, messageID int64, now time.Time) (RoomEvent, error) {
	result, err := s.db.Exec(`INSERT INTO room_events(room_id, event_type, message_id, created_at) VALUES(?, 'message_pinned', ?, ?)`, roomID, messageID, now.Unix())
	if err != nil {
		return RoomEvent{}, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return RoomEvent{}, err
	}
	row := s.db.QueryRow(`SELECT id, room_id, event_type, message_id, created_at FROM room_events WHERE id = ?`, id)
	return scanEvent(row)
}

func (s *MessageStore) AppendDeleteEvent(roomID int64, now time.Time) (RoomEvent, error) {
	result, err := s.db.Exec(`INSERT INTO room_events(room_id, event_type, created_at) VALUES(?, 'message_deleted', ?)`, roomID, now.Unix())
	if err != nil {
		return RoomEvent{}, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return RoomEvent{}, err
	}
	row := s.db.QueryRow(`SELECT id, room_id, event_type, message_id, created_at FROM room_events WHERE id = ?`, id)
	return scanEvent(row)
}

func (s *MessageStore) ListEventsSince(roomID int64, sinceID int64) ([]RoomEvent, error) {
	rows, err := s.db.Query(`SELECT id, room_id, event_type, message_id, created_at FROM room_events WHERE room_id = ? AND id > ? ORDER BY id ASC`, roomID, sinceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []RoomEvent
	for rows.Next() {
		event, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

func (s *MessageStore) ListExpiredFiles(now time.Time) ([]Message, error) {
	rows, err := s.db.Query(`SELECT id, room_id, type, COALESCE(text_content, ''), COALESCE(file_name, ''), COALESCE(file_path, ''), COALESCE(file_size, 0), COALESCE(mime_type, ''), is_pinned, created_at, expires_at FROM messages WHERE type = 'file' AND expires_at IS NOT NULL AND expires_at <= ?`, now.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []Message
	for rows.Next() {
		message, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		messages = append(messages, message)
	}
	return messages, rows.Err()
}

func (s *MessageStore) DeleteByID(id int64) error {
	_, err := s.db.Exec(`DELETE FROM messages WHERE id = ?`, id)
	return err
}

func (s *MessageStore) ClearFilePath(id int64) error {
	_, err := s.db.Exec(`UPDATE messages SET file_path = '' WHERE id = ?`, id)
	return err
}

func scanMessage(row scanner) (Message, error) {
	var message Message
	var expiresAt sql.NullInt64
	var isPinned int
	if err := row.Scan(&message.ID, &message.RoomID, &message.Type, &message.TextContent, &message.FileName, &message.FilePath, &message.FileSize, &message.MimeType, &isPinned, &message.CreatedAt, &expiresAt); err != nil {
		return Message{}, err
	}
	message.IsPinned = isPinned == 1
	if expiresAt.Valid {
		message.ExpiresAt = &expiresAt.Int64
	}
	return message, nil
}

func (s *MessageStore) TogglePin(id int64) (Message, error) {
	_, err := s.db.Exec(`UPDATE messages SET is_pinned = CASE WHEN is_pinned = 1 THEN 0 ELSE 1 END WHERE id = ?`, id)
	if err != nil {
		return Message{}, err
	}
	return s.Get(id)
}

func (s *MessageStore) DeleteMessage(id int64) error {
	_, err := s.db.Exec(`DELETE FROM messages WHERE id = ?`, id)
	return err
}

func (s *MessageStore) LatestEventID(roomID int64) int64 {
	var id sql.NullInt64
	if err := s.db.QueryRow(`SELECT MAX(id) FROM room_events WHERE room_id = ?`, roomID).Scan(&id); err != nil {
		return 0
	}
	if id.Valid {
		return id.Int64
	}
	return 0
}

func scanEvent(row scanner) (RoomEvent, error) {
	var event RoomEvent
	var messageID sql.NullInt64
	if err := row.Scan(&event.ID, &event.RoomID, &event.EventType, &messageID, &event.CreatedAt); err != nil {
		return RoomEvent{}, err
	}
	if messageID.Valid {
		event.MessageID = &messageID.Int64
	}
	return event, nil
}

type HourlyCount struct {
	Hour  string `json:"hour"`
	Count int    `json:"count"`
}

func (s *MessageStore) CountAll(nowUnix int64) (int, error) {
	var count int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE expires_at IS NULL OR expires_at > ?`, nowUnix).Scan(&count)
	return count, err
}

func (s *MessageStore) CountFiles(nowUnix int64) (int, error) {
	var count int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE type = 'file' AND (expires_at IS NULL OR expires_at > ?)`, nowUnix).Scan(&count)
	return count, err
}

func (s *MessageStore) GetHourlyCreation(sinceUnix int64) ([]HourlyCount, error) {
	rows, err := s.db.Query(`SELECT strftime('%H', datetime(created_at, 'unixepoch')) as hour, COUNT(*) as count FROM messages WHERE created_at > ? GROUP BY hour ORDER BY hour`, sinceUnix)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []HourlyCount
	for rows.Next() {
		var hc HourlyCount
		if err := rows.Scan(&hc.Hour, &hc.Count); err != nil {
			return nil, err
		}
		result = append(result, hc)
	}
	return result, rows.Err()
}
