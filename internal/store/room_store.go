package store

import (
	"database/sql"
	"fmt"
	"time"
)

type Room struct {
	ID           int64  `json:"id"`
	RoomCode     string `json:"roomCode"`
	RoomName     string `json:"roomName,omitempty"`
	Status       string `json:"status"`
	IsPermanent  bool   `json:"isPermanent"`
	CreatedAt    int64  `json:"createdAt"`
	LastActiveAt int64  `json:"lastActiveAt"`
	ClosedAt     *int64 `json:"closedAt,omitempty"`
	DeleteAfter  *int64 `json:"deleteAfter,omitempty"`
}

type RoomStats struct {
	Total      int `json:"total"`
	Permanent  int `json:"permanent"`
	Expiring1h int `json:"expiring1h"`
	Expiring24h int `json:"expiring24h"`
}

type RoomStore struct {
	db *sql.DB
}

func NewRoomStore(db *sql.DB) *RoomStore {
	return &RoomStore{db: db}
}

func (s *RoomStore) Create(roomCode string, ttlSec int64, now time.Time) (Room, error) {
	deleteAfter := now.Unix() + ttlSec
	if _, err := s.db.Exec(`INSERT INTO rooms(room_code, room_name, status, is_permanent, created_at, last_active_at, delete_after) VALUES(?, '', 'open', 0, ?, ?, ?)`, roomCode, now.Unix(), now.Unix(), deleteAfter); err != nil {
		return Room{}, err
	}
	return s.GetByCode(roomCode)
}

func (s *RoomStore) ListOpen() ([]Room, error) {
	return s.queryRooms(`SELECT id, room_code, COALESCE(room_name, ''), status, is_permanent, created_at, last_active_at, closed_at, delete_after FROM rooms WHERE status = 'open' ORDER BY created_at DESC`)
}

func (s *RoomStore) ListAll() ([]Room, error) {
	return s.queryRooms(`SELECT id, room_code, COALESCE(room_name, ''), status, is_permanent, created_at, last_active_at, closed_at, delete_after FROM rooms ORDER BY created_at DESC`)
}

func (s *RoomStore) CountAll() (int, error) {
	var count int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM rooms`).Scan(&count)
	return count, err
}

func (s *RoomStore) GetStats(nowUnix int64) (RoomStats, error) {
	var stats RoomStats
	// total
	s.db.QueryRow(`SELECT COUNT(*) FROM rooms`).Scan(&stats.Total)
	// permanent
	s.db.QueryRow(`SELECT COUNT(*) FROM rooms WHERE is_permanent = 1`).Scan(&stats.Permanent)
	// expiring in 1h (non-permanent, has deleteAfter, deleteAfter <= now + 3600)
	s.db.QueryRow(`SELECT COUNT(*) FROM rooms WHERE is_permanent = 0 AND delete_after IS NOT NULL AND delete_after <= ?`, nowUnix+3600).Scan(&stats.Expiring1h)
	// expiring in 24h
	s.db.QueryRow(`SELECT COUNT(*) FROM rooms WHERE is_permanent = 0 AND delete_after IS NOT NULL AND delete_after <= ?`, nowUnix+86400).Scan(&stats.Expiring24h)
	return stats, nil
}

func (s *RoomStore) GetHourlyCreation(sinceUnix int64) ([]HourlyCount, error) {
	rows, err := s.db.Query(`SELECT strftime('%H', datetime(created_at, 'unixepoch')) as hour, COUNT(*) as count FROM rooms WHERE created_at > ? GROUP BY hour ORDER BY hour`, sinceUnix)
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

func (s *RoomStore) ListAllPaginated(page, pageSize int) ([]Room, int, error) {
	total, err := s.CountAll()
	if err != nil {
		return nil, 0, err
	}
	offset := (page - 1) * pageSize
	rooms, err := s.queryRooms(`SELECT id, room_code, COALESCE(room_name, ''), status, is_permanent, created_at, last_active_at, closed_at, delete_after FROM rooms ORDER BY created_at DESC LIMIT ? OFFSET ?`, pageSize, offset)
	if err != nil {
		return nil, 0, err
	}
	return rooms, total, nil
}

func (s *RoomStore) GetByCode(roomCode string) (Room, error) {
	row := s.db.QueryRow(`SELECT id, room_code, COALESCE(room_name, ''), status, is_permanent, created_at, last_active_at, closed_at, delete_after FROM rooms WHERE room_code = ?`, roomCode)
	room, err := scanRoom(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return Room{}, ErrNotFound
		}
		return Room{}, err
	}
	return room, nil
}

func (s *RoomStore) GetByID(id int64) (Room, error) {
	row := s.db.QueryRow(`SELECT id, room_code, COALESCE(room_name, ''), status, is_permanent, created_at, last_active_at, closed_at, delete_after FROM rooms WHERE id = ?`, id)
	room, err := scanRoom(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return Room{}, ErrNotFound
		}
		return Room{}, err
	}
	return room, nil
}

func (s *RoomStore) UpdateNameByCode(roomCode string, roomName string) (Room, error) {
	_, err := s.db.Exec(`UPDATE rooms SET room_name = ? WHERE room_code = ?`, roomName, roomCode)
	if err != nil {
		return Room{}, err
	}
	return s.GetByCode(roomCode)
}

func (s *RoomStore) DeleteByCode(roomCode string) error {
	_, err := s.db.Exec(`DELETE FROM rooms WHERE room_code = ?`, roomCode)
	return err
}

func (s *RoomStore) BatchDeleteByCodes(roomCodes []string) error {
	for _, code := range roomCodes {
		if _, err := s.db.Exec(`DELETE FROM rooms WHERE room_code = ?`, code); err != nil {
			return fmt.Errorf("delete room %s: %w", code, err)
		}
	}
	return nil
}

func (s *RoomStore) Extend(roomCode string, extendSec int64) (Room, error) {
	room, err := s.GetByCode(roomCode)
	if err != nil {
		return Room{}, err
	}
	if room.DeleteAfter == nil {
		return room, nil
	}
	newDeleteAfter := *room.DeleteAfter + extendSec
	_, err = s.db.Exec(`UPDATE rooms SET delete_after = ? WHERE room_code = ?`, newDeleteAfter, roomCode)
	if err != nil {
		return Room{}, err
	}
	return s.GetByCode(roomCode)
}

func (s *RoomStore) SetPermanent(roomCode string) (Room, error) {
	_, err := s.db.Exec(`UPDATE rooms SET is_permanent = 1, delete_after = NULL WHERE room_code = ?`, roomCode)
	if err != nil {
		return Room{}, err
	}
	return s.GetByCode(roomCode)
}

func (s *RoomStore) PublicDeleteByCode(roomCode string) error {
	_, err := s.db.Exec(`DELETE FROM rooms WHERE room_code = ?`, roomCode)
	return err
}

func (s *RoomStore) queryRooms(query string, args ...interface{}) ([]Room, error) {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var rooms []Room
	for rows.Next() {
		room, err := scanRoom(rows)
		if err != nil {
			return nil, err
		}
		rooms = append(rooms, room)
	}
	return rooms, rows.Err()
}

type scanner interface {
	Scan(dest ...any) error
}

func scanRoom(row scanner) (Room, error) {
	var room Room
	var closedAt sql.NullInt64
	var deleteAfter sql.NullInt64
	var isPermanent int
	if err := row.Scan(&room.ID, &room.RoomCode, &room.RoomName, &room.Status, &isPermanent, &room.CreatedAt, &room.LastActiveAt, &closedAt, &deleteAfter); err != nil {
		return Room{}, err
	}
	room.IsPermanent = isPermanent == 1
	if closedAt.Valid {
		room.ClosedAt = &closedAt.Int64
	}
	if deleteAfter.Valid {
		room.DeleteAfter = &deleteAfter.Int64
	}
	return room, nil
}