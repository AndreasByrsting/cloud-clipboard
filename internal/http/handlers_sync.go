package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"cloud-clipboard/internal/realtime"
	"cloud-clipboard/internal/service"

	"github.com/gorilla/websocket"
)

type SyncHandler struct {
	hub   *realtime.Hub
	rooms *service.RoomService
}

func NewSyncHandler(hub *realtime.Hub, rooms *service.RoomService, _ *SessionStore) *SyncHandler {
	return &SyncHandler{hub: hub, rooms: rooms}
}

func (h *SyncHandler) RoomWebSocket(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) != 3 {
		http.NotFound(w, r)
		return
	}
	roomCode := parts[2]
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	h.hub.Register(roomCode, conn)
	defer func() {
		h.hub.Unregister(roomCode, conn)
		conn.Close()
	}()

	// Send welcome message with latest event ID
	lastEventID := h.rooms.LatestEventID(roomCode)
	welcome, _ := json.Marshal(map[string]any{
		"type":        "welcome",
		"lastEventId": lastEventID,
	})
	conn.WriteMessage(websocket.TextMessage, welcome)

	// Send pings every 30s to keep the connection alive
	done := make(chan struct{})
	defer close(done)
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(10*time.Second)); err != nil {
					return
				}
			case <-done:
				return
			}
		}
	}()

	_ = conn.SetReadDeadline(time.Now().Add(90 * time.Second))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(90 * time.Second))
	})

	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
	}
}
