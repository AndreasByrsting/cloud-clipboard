package service

import (
	"fmt"
	"math/rand"
	"mime"
	"strings"
	"time"

	"cloud-clipboard/internal/realtime"
	"cloud-clipboard/internal/store"
)

const roomAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

type RoomService struct {
	settings       *store.SettingsStore
	rooms          *store.RoomStore
	messages       *store.MessageStore
	uploadSessions *store.UploadSessionStore
	files          *FileService
	hub            *realtime.Hub
	rng            *rand.Rand
}

func NewRoomService(settings *store.SettingsStore, rooms *store.RoomStore, messages *store.MessageStore, uploadSessions *store.UploadSessionStore, files *FileService, hub *realtime.Hub) *RoomService {
	return &RoomService{
		settings:       settings,
		rooms:          rooms,
		messages:       messages,
		uploadSessions: uploadSessions,
		files:          files,
		hub:            hub,
		rng:            rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

func (s *RoomService) ListRooms() ([]store.Room, error) {
	return s.rooms.ListOpen()
}

func (s *RoomService) ListAllRooms() ([]store.Room, error) {
	return s.rooms.ListAll()
}

func (s *RoomService) ListAllRoomsPaginated(page, pageSize int) ([]store.Room, int, error) {
	return s.rooms.ListAllPaginated(page, pageSize)
}

func (s *RoomService) GetRoomStats(nowUnix int64) (store.RoomStats, error) {
	return s.rooms.GetStats(nowUnix)
}

func (s *RoomService) GetMessageStats() (total, files int, err error) {
	total, err = s.messages.CountAll()
	if err != nil {
		return 0, 0, err
	}
	files, err = s.messages.CountFiles()
	return total, files, err
}

func (s *RoomService) GetRoomHourlyCreation(sinceUnix int64) ([]store.HourlyCount, error) {
	return s.rooms.GetHourlyCreation(sinceUnix)
}

func (s *RoomService) GetMessageHourlyCreation(sinceUnix int64) ([]store.HourlyCount, error) {
	return s.messages.GetHourlyCreation(sinceUnix)
}

func (s *RoomService) CreateRoom(now time.Time) (store.Room, error) {
	length, err := s.settings.GetInt("room_code_length")
	if err != nil {
		return store.Room{}, err
	}
	ttlSec, err := s.settings.GetInt("room_default_ttl_sec")
	if err != nil {
		return store.Room{}, err
	}
	for i := 0; i < 10; i++ {
		code := s.randomCode(length)
		room, err := s.rooms.Create(code, int64(ttlSec), now)
		if err == nil {
			return room, nil
		}
	}
	return store.Room{}, fmt.Errorf("unable to allocate unique room code")
}

func (s *RoomService) ListMessages(roomCode string) ([]store.Message, error) {
	room, err := s.rooms.GetByCode(roomCode)
	if err != nil {
		return nil, err
	}
	return s.messages.ListByRoom(room.ID)
}

func (s *RoomService) PostTextMessage(roomCode string, text string, now time.Time) (store.Message, realtime.Event, error) {
	room, err := s.rooms.GetByCode(roomCode)
	if err != nil {
		return store.Message{}, realtime.Event{}, err
	}
	maxLength, err := s.settings.GetInt("max_message_text_length")
	if err != nil {
		return store.Message{}, realtime.Event{}, err
	}
	if len([]rune(strings.TrimSpace(text))) > maxLength {
		return store.Message{}, realtime.Event{}, fmt.Errorf("message exceeds configured length limit")
	}

	message, err := s.messages.Create(room.ID, text, now)
	if err != nil {
		return store.Message{}, realtime.Event{}, err
	}
	event, err := s.messages.AppendEvent(room.ID, message.ID, now)
	if err != nil {
		return store.Message{}, realtime.Event{}, err
	}

	realtimeEvent := realtime.Event{ID: event.ID, RoomCode: room.RoomCode, Type: event.EventType, MessageID: message.ID, CreatedAt: event.CreatedAt}
	s.hub.Broadcast(room.RoomCode, realtimeEvent)
	return message, realtimeEvent, nil
}

func (s *RoomService) PostFileMessage(roomCode string, originalName string, content []byte, now time.Time) (store.Message, realtime.Event, error) {
	room, err := s.rooms.GetByCode(roomCode)
	if err != nil {
		return store.Message{}, realtime.Event{}, err
	}
	if s.files == nil {
		return store.Message{}, realtime.Event{}, fmt.Errorf("file service is not configured")
	}
	stored, err := s.files.Save(originalName, content, now)
	if err != nil {
		return store.Message{}, realtime.Event{}, err
	}
	var expiresAt *time.Time
	fileNeverExpire := strings.ToLower(strings.TrimSpace(s.settings.MustGet("file_never_expire"))) == "true"
	if !fileNeverExpire {
		expireSec, err := s.settings.GetInt("file_message_expire_sec")
		if err != nil {
			_ = s.files.Delete(stored.StoredPath)
			return store.Message{}, realtime.Event{}, err
		}
		t := now.Add(time.Duration(expireSec) * time.Second)
		expiresAt = &t
	}
	mimeType := mime.TypeByExtension(filepathExt(stored.OriginalName))
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	message, err := s.messages.CreateFile(room.ID, stored.OriginalName, stored.StoredPath, stored.Size, mimeType, expiresAt, now)
	if err != nil {
		_ = s.files.Delete(stored.StoredPath)
		return store.Message{}, realtime.Event{}, err
	}
	event, err := s.messages.AppendFileEvent(room.ID, message.ID, now)
	if err != nil {
		return store.Message{}, realtime.Event{}, err
	}
	realtimeEvent := realtime.Event{ID: event.ID, RoomCode: room.RoomCode, Type: event.EventType, MessageID: message.ID, CreatedAt: event.CreatedAt}
	s.hub.Broadcast(room.RoomCode, realtimeEvent)
	return message, realtimeEvent, nil
}

func (s *RoomService) PostStoredFileMessage(roomCode string, originalName string, storedPath string, size int64, now time.Time) (store.Message, realtime.Event, error) {
	room, err := s.rooms.GetByCode(roomCode)
	if err != nil {
		return store.Message{}, realtime.Event{}, err
	}
	var expiresAt *time.Time
	fileNeverExpire := strings.ToLower(strings.TrimSpace(s.settings.MustGet("file_never_expire"))) == "true"
	if !fileNeverExpire {
		expireSec, err := s.settings.GetInt("file_message_expire_sec")
		if err != nil {
			return store.Message{}, realtime.Event{}, err
		}
		t := now.Add(time.Duration(expireSec) * time.Second)
		expiresAt = &t
	}
	mimeType := mime.TypeByExtension(filepathExt(originalName))
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	message, err := s.messages.CreateFile(room.ID, originalName, storedPath, size, mimeType, expiresAt, now)
	if err != nil {
		return store.Message{}, realtime.Event{}, err
	}
	event, err := s.messages.AppendFileEvent(room.ID, message.ID, now)
	if err != nil {
		return store.Message{}, realtime.Event{}, err
	}
	realtimeEvent := realtime.Event{ID: event.ID, RoomCode: room.RoomCode, Type: event.EventType, MessageID: message.ID, CreatedAt: event.CreatedAt}
	s.hub.Broadcast(room.RoomCode, realtimeEvent)
	return message, realtimeEvent, nil
}

func (s *RoomService) GetFileMessage(messageID int64) (store.Message, error) {
	message, err := s.messages.Get(messageID)
	if err != nil {
		return store.Message{}, err
	}
	if message.Type != "file" {
		return store.Message{}, fmt.Errorf("message is not a file")
	}
	return message, nil
}

func (s *RoomService) ListEvents(roomCode string, sinceID int64) ([]realtime.Event, error) {
	room, err := s.rooms.GetByCode(roomCode)
	if err != nil {
		return nil, err
	}
	events, err := s.messages.ListEventsSince(room.ID, sinceID)
	if err != nil {
		return nil, err
	}
	result := make([]realtime.Event, 0, len(events))
	for _, event := range events {
		messageID := int64(0)
		if event.MessageID != nil {
			messageID = *event.MessageID
		}
		result = append(result, realtime.Event{ID: event.ID, RoomCode: room.RoomCode, Type: event.EventType, MessageID: messageID, CreatedAt: event.CreatedAt})
	}
	return result, nil
}

func (s *RoomService) DeleteRoom(roomCode string) error {
	return s.deleteRoom(roomCode, false)
}

func (s *RoomService) BatchDeleteRooms(roomCodes []string) (int, error) {
	deleted := 0
	for _, code := range roomCodes {
		if err := s.deleteRoom(code, false); err != nil {
			return deleted, err
		}
		deleted++
	}
	return deleted, nil
}

func (s *RoomService) HasOnline(roomCode string) bool {
	return s.hub.OnlineCount(roomCode) > 0
}

func (s *RoomService) GetRoom(roomCode string) (store.Room, error) {
	return s.rooms.GetByCode(roomCode)
}

func (s *RoomService) UpdateRoomName(roomCode string, roomName string) (store.Room, error) {
	trimmed := strings.TrimSpace(roomName)
	if len([]rune(trimmed)) > 40 {
		return store.Room{}, fmt.Errorf("room name must be 40 characters or fewer")
	}
	return s.rooms.UpdateNameByCode(roomCode, trimmed)
}

func (s *RoomService) ExtendRoom(roomCode string) (store.Room, error) {
	room, err := s.rooms.GetByCode(roomCode)
	if err != nil {
		return store.Room{}, err
	}
	if room.IsPermanent {
		return store.Room{}, fmt.Errorf("permanent room does not need extension")
	}
	if room.DeleteAfter == nil {
		return store.Room{}, fmt.Errorf("room has no expiration")
	}
	canExtendSec, err := s.settings.GetInt("room_can_extend_sec")
	if err != nil {
		return store.Room{}, err
	}
	extendSec, err := s.settings.GetInt("room_extend_sec")
	if err != nil {
		return store.Room{}, err
	}
	now := time.Now().UTC().Unix()
	remaining := *room.DeleteAfter - now
	if remaining > int64(canExtendSec) {
		return store.Room{}, fmt.Errorf("room still has more than %d hours remaining", canExtendSec/3600)
	}
	return s.rooms.Extend(roomCode, int64(extendSec))
}

func (s *RoomService) SetRoomPermanent(roomCode string) (store.Room, error) {
	room, err := s.rooms.GetByCode(roomCode)
	if err != nil {
		return store.Room{}, err
	}
	if room.IsPermanent {
		return room, nil
	}
	return s.rooms.SetPermanent(roomCode)
}

func (s *RoomService) PublicDeleteRoom(roomCode string) error {
	return s.deleteRoom(roomCode, true)
}

func (s *RoomService) TogglePinMessage(messageID int64) (store.Message, realtime.Event, error) {
	message, err := s.messages.TogglePin(messageID)
	if err != nil {
		return store.Message{}, realtime.Event{}, err
	}
	room, err := s.rooms.GetByID(message.RoomID)
	if err != nil {
		return message, realtime.Event{}, err
	}
	now := time.Now().UTC()
	eventType := "message_unpinned"
	if message.IsPinned {
		eventType = "message_pinned"
	}
	event, err := s.messages.AppendPinEvent(message.RoomID, message.ID, now)
	if err != nil {
		return message, realtime.Event{}, err
	}
	realtimeEvent := realtime.Event{ID: event.ID, RoomCode: room.RoomCode, Type: eventType, MessageID: message.ID, CreatedAt: event.CreatedAt}
	s.hub.Broadcast(room.RoomCode, realtimeEvent)
	return message, realtimeEvent, nil
}

func (s *RoomService) DeleteMessage(messageID int64) (realtime.Event, error) {
	message, err := s.messages.Get(messageID)
	if err != nil {
		return realtime.Event{}, err
	}
	room, err := s.rooms.GetByID(message.RoomID)
	if err != nil {
		return realtime.Event{}, err
	}
	now := time.Now().UTC()
	if message.Type == "file" && s.files != nil {
		if err := s.files.Delete(message.FilePath); err != nil {
			return realtime.Event{}, err
		}
	}
	if err := s.messages.DeleteMessage(messageID); err != nil {
		return realtime.Event{}, err
	}
	event, err := s.messages.AppendDeleteEvent(message.RoomID, now)
	if err != nil {
		return realtime.Event{}, err
	}
	realtimeEvent := realtime.Event{ID: event.ID, RoomCode: room.RoomCode, Type: event.EventType, MessageID: messageID, CreatedAt: event.CreatedAt}
	s.hub.Broadcast(room.RoomCode, realtimeEvent)
	return realtimeEvent, nil
}

func (s *RoomService) DeleteExpiredRooms(now time.Time) error {
	rooms, err := s.rooms.ListAll()
	if err != nil {
		return err
	}
	for _, room := range rooms {
		if room.IsPermanent || room.DeleteAfter == nil || *room.DeleteAfter > now.Unix() {
			continue
		}
		if err := s.deleteRoom(room.RoomCode, false); err != nil {
			return err
		}
	}
	return nil
}

func (s *RoomService) deleteRoom(roomCode string, public bool) error {
	room, err := s.rooms.GetByCode(roomCode)
	if err != nil {
		return err
	}
	messages, err := s.messages.ListByRoom(room.ID)
	if err != nil {
		return err
	}
	if s.files != nil {
		for _, message := range messages {
			if message.Type != "file" || strings.TrimSpace(message.FilePath) == "" {
				continue
			}
			if err := s.files.Delete(message.FilePath); err != nil {
				return err
			}
		}
		sessions, err := s.uploadSessions.ListByRoomCode(roomCode)
		if err != nil {
			return err
		}
		for _, session := range sessions {
			if strings.TrimSpace(session.StoredPath) == "" {
				continue
			}
			if err := s.files.Delete(session.StoredPath); err != nil {
				return err
			}
		}
	}
	if err := s.uploadSessions.DeleteByRoomCode(roomCode); err != nil {
		return err
	}
	now := time.Now().UTC().Unix()
	s.hub.Broadcast(roomCode, realtime.Event{RoomCode: roomCode, Type: "room_deleted", CreatedAt: now})
	if public {
		return s.rooms.PublicDeleteByCode(roomCode)
	}
	return s.rooms.DeleteByCode(roomCode)
}

func (s *RoomService) randomCode(length int) string {
	buf := make([]byte, length)
	for i := range buf {
		buf[i] = roomAlphabet[s.rng.Intn(len(roomAlphabet))]
	}
	return string(buf)
}

func (s *RoomService) LatestEventID(roomCode string) int64 {
	room, err := s.rooms.GetByCode(roomCode)
	if err != nil {
		return 0
	}
	return s.messages.LatestEventID(room.ID)
}

func filepathExt(name string) string {
	for i := len(name) - 1; i >= 0; i-- {
		if name[i] == '.' {
			return name[i:]
		}
	}
	return ""
}
