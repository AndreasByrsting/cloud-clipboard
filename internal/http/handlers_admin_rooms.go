package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"cloud-clipboard/internal/config"
	"cloud-clipboard/internal/service"
	"cloud-clipboard/internal/store"
)

type AdminRoomHandler struct {
	rooms      *service.RoomService
	statistics *store.StatisticsStore
}

func NewAdminRoomHandler(rooms *service.RoomService, statistics *store.StatisticsStore) *AdminRoomHandler {
	return &AdminRoomHandler{rooms: rooms, statistics: statistics}
}

func (h *AdminRoomHandler) Manage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/api/admin/rooms" {
		switch r.Method {
		case http.MethodGet:
			page, _ := strconv.Atoi(r.URL.Query().Get("page"))
			pageSize, _ := strconv.Atoi(r.URL.Query().Get("pageSize"))
			if page < 1 { page = 1 }
			if pageSize < 10 { pageSize = 10 }
			if pageSize > 500 { pageSize = 500 }
			rooms, total, err := h.rooms.ListAllRoomsPaginated(page, pageSize)
			if err != nil {
				errorJSON(w, http.StatusInternalServerError, err.Error())
				return
			}
			now := time.Now().In(config.TimeLocation)
			nowUnix := now.Unix()
			stats, _ := h.rooms.GetRoomStats(nowUnix)
			totalMsgs, totalFiles, _ := h.rooms.GetMessageStats()

			// 从统计表读取图表数据
			var roomChart, msgChart []map[string]any
			if h.statistics != nil {
				hourly, _ := h.statistics.GetRecent(now)
				for _, s := range hourly {
					// 只取小时部分（"2026-06-27 14" -> "14"）
					hourLabel := s.Hour
					if len(s.Hour) >= 13 {
						hourLabel = s.Hour[11:13]
					}
					roomChart = append(roomChart, map[string]any{"hour": hourLabel, "count": s.RoomCount})
					msgChart = append(msgChart, map[string]any{"hour": hourLabel, "count": s.MessageCount})
				}
			}

			writeJSON(w, http.StatusOK, map[string]any{
				"rooms":    rooms,
				"total":    total,
				"page":     page,
				"pageSize": pageSize,
				"stats": map[string]any{
					"totalRooms":    stats.Total,
					"permanentRooms": stats.Permanent,
					"totalMessages": totalMsgs,
					"totalFiles":    totalFiles,
					"roomChart":     roomChart,
					"messageChart":  msgChart,
				},
			})
		case http.MethodDelete:
			// 批量删除房间
			var req struct {
				RoomCodes []string `json:"roomCodes"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				errorJSON(w, http.StatusBadRequest, "invalid JSON body")
				return
			}
			if len(req.RoomCodes) == 0 {
				errorJSON(w, http.StatusBadRequest, "roomCodes is required")
				return
			}
			count, err := h.rooms.BatchDeleteRooms(req.RoomCodes)
			if err != nil {
				errorJSON(w, http.StatusBadRequest, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"deleted": count})
		default:
			errorJSON(w, http.StatusMethodNotAllowed, "method not allowed")
		}
		return
	}

	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) != 5 {
		http.NotFound(w, r)
		return
	}
	roomCode := parts[3]
	action := parts[4]

	switch {
	case r.Method == http.MethodDelete && action == "delete":
		if err := h.rooms.DeleteRoom(roomCode); err != nil {
			errorJSON(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	default:
		http.NotFound(w, r)
	}
}