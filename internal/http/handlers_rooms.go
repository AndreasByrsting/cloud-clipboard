package httpapi

import (
	"io"
	"net/http"
	"strings"

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
}

func NewRoomHandler(rooms *service.RoomService, settings *service.SettingsService, files *service.FileService, sessions *store.UploadSessionStore, cfg config.Config) *RoomHandler {
	return &RoomHandler{rooms: rooms, settings: settings, files: files, sessions: sessions, defaultMaxUploadBytes: cfg.MaxUploadBytes}
}

func (h *RoomHandler) ListOrCreate(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		rooms, err := h.rooms.ListRooms()
		if err != nil {
			errorJSON(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"rooms": rooms})
	case http.MethodPost:
		room, err := h.rooms.CreateRoom(nowUTC())
		if err != nil {
			errorJSON(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, room)
	default:
		errorJSON(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *RoomHandler) Messages(w http.ResponseWriter, r *http.Request) {
	roomCode, err := roomCodeFromPath(r.URL.Path)
	if err != nil {
		errorJSON(w, http.StatusBadRequest, err.Error())
		return
	}

	switch r.Method {
	case http.MethodGet:
		messages, err := h.rooms.ListMessages(roomCode)
		if err != nil {
			errorJSON(w, http.StatusNotFound, err.Error())
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
			errorJSON(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"message": message, "event": event})
	default:
		errorJSON(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *RoomHandler) UploadFile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		errorJSON(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	roomCode, err := roomCodeFromPath(r.URL.Path)
	if err != nil {
		errorJSON(w, http.StatusBadRequest, err.Error())
		return
	}
	maxUploadBytes := h.defaultMaxUploadBytes
	if h.settings != nil {
		if settings, err := h.settings.GetAll(); err == nil && settings.MaxUploadBytes > 0 {
			maxUploadBytes = int64(settings.MaxUploadBytes)
		}
	}
	if err := r.ParseMultipartForm(maxUploadBytes); err != nil {
		errorJSON(w, http.StatusBadRequest, err.Error())
		return
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
		errorJSON(w, http.StatusBadRequest, "file exceeds configured size limit")
		return
	}
	message, event, err := h.rooms.PostFileMessage(roomCode, header.Filename, content, nowUTC())
	if err != nil {
		errorJSON(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"message": message, "event": event})
}

func (h *RoomHandler) CreateUploadSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		errorJSON(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	roomCode, err := roomCodeFromPath(r.URL.Path)
	if err != nil {
		errorJSON(w, http.StatusBadRequest, err.Error())
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
	if strings.TrimSpace(req.FileName) == "" || req.FileSize <= 0 {
		errorJSON(w, http.StatusBadRequest, "invalid upload session payload")
		return
	}
	stored, err := h.files.CreateChunkTarget(req.FileName, nowUTC())
	if err != nil {
		errorJSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	sessionID, err := service.GenerateToken()
	if err != nil {
		errorJSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	session := store.UploadSession{
		ID:           sessionID,
		RoomCode:     roomCode,
		FileName:     req.FileName,
		FileSize:     req.FileSize,
		MimeType:     req.MimeType,
		ChunkSize:    defaultChunkSize,
		UploadedSize: 0,
		StoredPath:   stored.StoredPath,
		CreatedAt:    nowUTC().Unix(),
		UpdatedAt:    nowUTC().Unix(),
	}
	if err := h.sessions.Create(session); err != nil {
		errorJSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, session)
}

func (h *RoomHandler) UploadChunk(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		errorJSON(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 5 {
		errorJSON(w, http.StatusBadRequest, "invalid upload path")
		return
	}
	uploadID := parts[3]
	session, err := h.sessions.Get(uploadID)
	if err != nil {
		errorJSON(w, http.StatusNotFound, err.Error())
		return
	}
	content, err := io.ReadAll(io.LimitReader(r.Body, session.ChunkSize+1))
	if err != nil {
		errorJSON(w, http.StatusBadRequest, err.Error())
		return
	}
	if int64(len(content)) > session.ChunkSize {
		errorJSON(w, http.StatusBadRequest, "chunk exceeds configured chunk size")
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

func (h *RoomHandler) FinalizeUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		errorJSON(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 5 {
		errorJSON(w, http.StatusBadRequest, "invalid upload path")
		return
	}
	uploadID := parts[3]
	session, err := h.sessions.Get(uploadID)
	if err != nil {
		errorJSON(w, http.StatusNotFound, err.Error())
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
	message, event, err := h.rooms.PostStoredFileMessage(session.RoomCode, stored.OriginalName, stored.StoredPath, stored.Size, nowUTC())
	if err != nil {
		errorJSON(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.sessions.Delete(uploadID); err != nil {
		errorJSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"message": message, "event": event})
}

func (h *RoomHandler) Events(w http.ResponseWriter, r *http.Request) {
	roomCode, err := roomCodeFromPath(r.URL.Path)
	if err != nil {
		errorJSON(w, http.StatusBadRequest, err.Error())
		return
	}
	sinceID, err := sinceEventID(r)
	if err != nil {
		errorJSON(w, http.StatusBadRequest, "invalid since_event_id")
		return
	}
	events, err := h.rooms.ListEvents(roomCode, sinceID)
	if err != nil {
		errorJSON(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": events})
}

func (h *RoomHandler) RoomDetail(w http.ResponseWriter, r *http.Request) {
	roomCode, err := roomCodeFromPath(r.URL.Path)
	if err != nil {
		errorJSON(w, http.StatusBadRequest, err.Error())
		return
	}
	room, err := h.rooms.GetRoom(roomCode)
	if err != nil {
		errorJSON(w, http.StatusNotFound, err.Error())
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

func (h *RoomHandler) UpdateRoomName(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		errorJSON(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	roomCode, err := roomCodeFromPath(r.URL.Path)
	if err != nil {
		errorJSON(w, http.StatusBadRequest, err.Error())
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
		errorJSON(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, room)
}

func (h *RoomHandler) ExtendRoom(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		errorJSON(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	roomCode, err := roomCodeFromPath(r.URL.Path)
	if err != nil {
		errorJSON(w, http.StatusBadRequest, err.Error())
		return
	}
	room, err := h.rooms.ExtendRoom(roomCode)
	if err != nil {
		errorJSON(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, room)
}

func (h *RoomHandler) SetPermanentRoom(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		errorJSON(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	roomCode, err := roomCodeFromPath(r.URL.Path)
	if err != nil {
		errorJSON(w, http.StatusBadRequest, err.Error())
		return
	}
	room, err := h.rooms.SetRoomPermanent(roomCode)
	if err != nil {
		errorJSON(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, room)
}

func (h *RoomHandler) PublicDeleteRoom(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		errorJSON(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	roomCode, err := roomCodeFromPath(r.URL.Path)
	if err != nil {
		errorJSON(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.rooms.PublicDeleteRoom(roomCode); err != nil {
		errorJSON(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (h *RoomHandler) TogglePinMessage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		errorJSON(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	messageID, err := messageIDFromPath(r.URL.Path)
	if err != nil {
		errorJSON(w, http.StatusBadRequest, err.Error())
		return
	}
	message, event, err := h.rooms.TogglePinMessage(messageID)
	if err != nil {
		errorJSON(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"message": message, "event": event})
}

func (h *RoomHandler) DeleteMessage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		errorJSON(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	messageID, err := messageIDFromPath(r.URL.Path)
	if err != nil {
		errorJSON(w, http.StatusBadRequest, err.Error())
		return
	}
	event, err := h.rooms.DeleteMessage(messageID)
	if err != nil {
		errorJSON(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "event": event})
}
