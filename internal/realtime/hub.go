package realtime

import (
	"encoding/json"
	"sync"

	"github.com/gorilla/websocket"
)

type Event struct {
	ID        int64  `json:"id"`
	RoomCode  string `json:"roomCode"`
	Type      string `json:"type"`
	MessageID int64  `json:"messageId,omitempty"`
	CreatedAt int64  `json:"createdAt"`
	Room      any    `json:"room,omitempty"`
}

type Hub struct {
	mu    sync.RWMutex
	rooms map[string]map[*websocket.Conn]struct{}
}

func NewHub() *Hub {
	return &Hub{
		rooms: make(map[string]map[*websocket.Conn]struct{}),
	}
}

func (h *Hub) Register(roomCode string, conn *websocket.Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.rooms[roomCode] == nil {
		h.rooms[roomCode] = make(map[*websocket.Conn]struct{})
	}
	h.rooms[roomCode][conn] = struct{}{}
}

func (h *Hub) Unregister(roomCode string, conn *websocket.Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()

	clients := h.rooms[roomCode]
	if clients == nil {
		return
	}
	delete(clients, conn)
	if len(clients) == 0 {
		delete(h.rooms, roomCode)
	}
}

func (h *Hub) Broadcast(roomCode string, event Event) {
	payload, err := json.Marshal(event)
	if err != nil {
		return
	}

	h.mu.RLock()
	clients := h.rooms[roomCode]
	conns := make([]*websocket.Conn, 0, len(clients))
	for conn := range clients {
		conns = append(conns, conn)
	}
	h.mu.RUnlock()

	for _, conn := range conns {
		if err := conn.WriteMessage(websocket.TextMessage, payload); err != nil {
			h.Unregister(roomCode, conn)
			conn.Close()
		}
	}
}

func (h *Hub) OnlineCount(roomCode string) int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.rooms[roomCode])
}
