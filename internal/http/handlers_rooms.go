package httpapi

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"

	"cloud-clipboard/internal/config"
	"cloud-clipboard/internal/service"
	"cloud-clipboard/internal/store"
)

const defaultChunkSize int64 = 5 * 1024 * 1024

type RoomHandler struct {
	rooms                 *service.RoomService
	settings              *service.SettingsService
	files                 *service.FileService
	sessions              *store.UploadSessionStore
	defaultMaxUploadBytes int64
	uploadLocks           [64]sync.Mutex
}

func NewRoomHandler(rooms *service.RoomService, settings *service.SettingsService, files *service.FileService, sessions *store.UploadSessionStore, cfg config.Config) *RoomHandler {
	return &RoomHandler{rooms: rooms, settings: settings, files: files, sessions: sessions, defaultMaxUploadBytes: cfg.MaxUploadBytes}
}

func (h *RoomHandler) ListOrCreate(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		room, err := h.rooms.CreateRoom(nowUTC())
		if err != nil {
			errorJSON(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, room)
	default:
		// The former public room listing is intentionally not available.
		errorJSON(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *RoomHandler) Messages(w http.ResponseWriter, r *http.Request, roomCode string) {
	switch r.Method {
	case http.MethodGet:
		messages, err := h.rooms.ListMessages(roomCode)
		if err != nil {
			writeRoomError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"messages": messages})
	case http.MethodPost:
		var req struct {
			Text string `json:"text"`
		}
		if err := readJSON(r, &req); err != nil {
			errorJSON(w, http.StatusBadRequest, "invalid request body")
			return
		}
		message, event, err := h.rooms.PostTextMessage(roomCode, req.Text, nowUTC())
		if err != nil {
			writeRoomError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"message": message, "event": event})
	default:
		errorJSON(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *RoomHandler) UploadFile(w http.ResponseWriter, r *http.Request, roomCode string) {
	if r.Method != http.MethodPost {
		errorJSON(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	maxUploadBytes := h.maxUploadBytes()

	// Cap the entire multipart body, not only the extracted file. This also
	// protects ParseMultipartForm from buffering oversized requests.
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes+(1<<20))
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		errorJSON(w, http.StatusBadRequest, "invalid multipart body")
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		errorJSON(w, http.StatusBadRequest, "missing file")
		return
	}
	defer file.Close()
	content, err := io.ReadAll(io.LimitReader(file, maxUploadBytes+1))
	if err != nil {
		errorJSON(w, http.StatusBadRequest, err.Error())
		return
	}
	if int64(len(content)) > maxUploadBytes {
		errorJSON(w, http.StatusRequestEntityTooLarge, "file exceeds configured size limit")
		return
	}
	message, event, err := h.rooms.PostFileMessage(roomCode, header.Filename, content, nowUTC())
	if err != nil {
		writeRoomError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"message": message, "event": event})
}

func (h *RoomHandler) CreateUploadSession(w http.ResponseWriter, r *http.Request, roomCode string) {
	if r.Method != http.MethodPost {
		errorJSON(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req struct {
		FileName string `json:"fileName"`
		FileSize int64  `json:"fileSize"`
		MimeType string `json:"mimeType"`
	}
	if err := readJSON(r, &req); err != nil {
		errorJSON(w, http.StatusBadRequest, "invalid request body")
		return
	}
	fileName := strings.TrimSpace(req.FileName)
	if fileName == "" || req.FileSize <= 0 {
		errorJSON(w, http.StatusBadRequest, "invalid upload session payload")
		return
	}
	if strings.ContainsAny(fileName, `/\`) {
		errorJSON(w, http.StatusBadRequest, "invalid file name")
		return
	}
	// Do not create partial files for room codes that do not exist.
	if _, err := h.rooms.GetRoom(roomCode); err != nil {
		writeRoomError(w, err)
		return
	}
	if limit := h.maxUploadBytes(); req.FileSize > limit {
		errorJSON(w, http.StatusRequestEntityTooLarge, "file exceeds configured size limit")
		return
	}
	stored, err := h.files.CreateChunkTarget(fileName, nowUTC())
	if err != nil {
		errorJSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	sessionID, err := service.GenerateToken()
	if err != nil {
		_ = h.files.Delete(stored.StoredPath)
		errorJSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	now := nowUTC()
	session := store.UploadSession{
		ID:           sessionID,
		RoomCode:     roomCode,
		FileName:     fileName,
		FileSize:     req.FileSize,
		MimeType:     strings.TrimSpace(req.MimeType),
		ChunkSize:    defaultChunkSize,
		UploadedSize: 0,
		StoredPath:   stored.StoredPath,
		CreatedAt:    now.Unix(),
		UpdatedAt:    now.Unix(),
	}
	if err := h.sessions.Create(session); err != nil {
		_ = h.files.Delete(stored.StoredPath)
		errorJSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, session)
}

func (h *RoomHandler) UploadChunk(w http.ResponseWriter, r *http.Request, roomCode string, uploadID string) {
	if r.Method != http.MethodPut {
		errorJSON(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	unlock := h.lockUpload(uploadID)
	defer unlock()

	session, err := h.sessionForRoom(uploadID, roomCode)
	if err != nil {
		errorJSON(w, http.StatusNotFound, "upload session not found")
		return
	}
	content, err := io.ReadAll(io.LimitReader(r.Body, session.ChunkSize+1))
	if err != nil {
		errorJSON(w, http.StatusBadRequest, err.Error())
		return
	}
	if int64(len(content)) > session.ChunkSize {
		errorJSON(w, http.StatusRequestEntityTooLarge, "chunk exceeds configured chunk size")
		return
	}
	if session.UploadedSize+int64(len(content)) > session.FileSize {
		errorJSON(w, http.StatusRequestEntityTooLarge, "upload exceeds declared file size")
		return
	}
	if err := h.files.AppendChunk(session.StoredPath, content); err != nil {
		errorJSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	session.UploadedSize += int64(len(content))
	if err := h.sessions.UpdateProgress(session.ID, session.UploadedSize, nowUTC()); err != nil {
		errorJSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "uploadedSize": session.UploadedSize})
}

func (h *RoomHandler) FinalizeUpload(w http.ResponseWriter, r *http.Request, roomCode string, uploadID string) {
	if r.Method != http.MethodPost {
		errorJSON(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	unlock := h.lockUpload(uploadID)
	defer func() {
		unlock()
	}()

	session, err := h.sessionForRoom(uploadID, roomCode)
	if err != nil {
		errorJSON(w, http.StatusNotFound, "upload session not found")
		return
	}
	if session.UploadedSize != session.FileSize {
		errorJSON(w, http.StatusBadRequest, "uploaded size does not match file size")
		return
	}
	stored, err := h.files.FinalizeChunkTarget(session.StoredPath, session.FileName)
	if err != nil {
		errorJSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	message, event, err := h.rooms.PostStoredFileMessage(roomCode, stored.OriginalName, stored.StoredPath, stored.Size, nowUTC())
	if err != nil {
		writeRoomError(w, err)
		return
	}
	if err := h.sessions.Delete(uploadID); err != nil {
		errorJSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"message": message, "event": event})
}

func (h *RoomHandler) Events(w http.ResponseWriter, r *http.Request, roomCode string) {
	if r.Method != http.MethodGet {
		errorJSON(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	sinceID, err := sinceEventID(r)
	if err != nil {
		errorJSON(w, http.StatusBadRequest, "invalid since_event_id")
		return
	}
	events, err := h.rooms.ListEvents(roomCode, sinceID)
	if err != nil {
		writeRoomError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": events})
}

func (h *RoomHandler) RoomDetail(w http.ResponseWriter, r *http.Request, roomCode string) {
	if r.Method != http.MethodGet {
		errorJSON(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	room, err := h.rooms.GetRoom(roomCode)
	if err != nil {
		writeRoomError(w, err)
		return
	}
	settings, err := h.settings.GetPublic()
	if err != nil {
		errorJSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":                   room.ID,
		"roomCode":             room.RoomCode,
		"roomName":             room.RoomName,
		"status":               room.Status,
		"isPermanent":          room.IsPermanent,
		"createdAt":            room.CreatedAt,
		"lastActiveAt":         room.LastActiveAt,
		"closedAt":             room.ClosedAt,
		"deleteAfter":          room.DeleteAfter,
		"maxUploadBytes":       settings.MaxUploadBytes,
		"maxMessageTextLength": settings.MaxMessageTextLength,
	})
}

func (h *RoomHandler) UpdateRoomName(w http.ResponseWriter, r *http.Request, roomCode string) {
	if r.Method != http.MethodPost {
		errorJSON(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req struct {
		RoomName string `json:"roomName"`
	}
	if err := readJSON(r, &req); err != nil {
		errorJSON(w, http.StatusBadRequest, "invalid request body")
		return
	}
	room, err := h.rooms.UpdateRoomName(roomCode, req.RoomName)
	if err != nil {
		writeRoomError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, room)
}

func (h *RoomHandler) ExtendRoom(w http.ResponseWriter, r *http.Request, roomCode string) {
	if r.Method != http.MethodPost {
		errorJSON(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	room, err := h.rooms.ExtendRoom(roomCode)
	if err != nil {
		writeRoomError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, room)
}

func (h *RoomHandler) SetPermanentRoom(w http.ResponseWriter, r *http.Request, roomCode string) {
	if r.Method != http.MethodPost {
		errorJSON(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	room, err := h.rooms.SetRoomPermanent(roomCode)
	if err != nil {
		writeRoomError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, room)
}

func (h *RoomHandler) PublicDeleteRoom(w http.ResponseWriter, r *http.Request, roomCode string) {
	if r.Method != http.MethodDelete {
		errorJSON(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if err := h.rooms.PublicDeleteRoom(roomCode); err != nil {
		writeRoomError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (h *RoomHandler) TogglePinMessage(w http.ResponseWriter, r *http.Request, roomCode string, messageID int64) {
	if r.Method != http.MethodPost {
		errorJSON(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	message, event, err := h.rooms.TogglePinMessage(roomCode, messageID)
	if err != nil {
		writeRoomError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"message": message, "event": event})
}

func (h *RoomHandler) DeleteMessage(w http.ResponseWriter, r *http.Request, roomCode string, messageID int64) {
	if r.Method != http.MethodDelete {
		errorJSON(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	event, err := h.rooms.DeleteMessage(roomCode, messageID)
	if err != nil {
		writeRoomError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "event": event})
}

func (h *RoomHandler) maxUploadBytes() int64 {
	if h.settings != nil {
		if settings, err := h.settings.GetAll(); err == nil && settings.MaxUploadBytes > 0 {
			return int64(settings.MaxUploadBytes)
		}
	}
	return h.defaultMaxUploadBytes
}

func (h *RoomHandler) lockUpload(uploadID string) func() {
	// Fixed-size stripes avoid an unbounded map of per-upload mutexes when
	// clients create many sessions and never finalize them.
	var hash uint32 = 2166136261
	for i := 0; i < len(uploadID); i++ {
		hash ^= uint32(uploadID[i])
		hash *= 16777619
	}
	mutex := &h.uploadLocks[int(hash)%len(h.uploadLocks)]
	mutex.Lock()
	return mutex.Unlock
}

func (h *RoomHandler) sessionForRoom(uploadID string, roomCode string) (store.UploadSession, error) {
	session, err := h.sessions.Get(uploadID)
	if err != nil {
		return store.UploadSession{}, err
	}
	if !strings.EqualFold(session.RoomCode, roomCode) {
		return store.UploadSession{}, store.ErrNotFound
	}
	return session, nil
}

func writeRoomError(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrNotFound) {
		errorJSON(w, http.StatusNotFound, "房间或消息不存在")
		return
	}
	errorJSON(w, http.StatusBadRequest, err.Error())
}
