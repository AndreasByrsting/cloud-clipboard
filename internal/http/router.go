package httpapi

import (
	"io/fs"
	"net/http"
	"strconv"
	"strings"

	appstate "cloud-clipboard/internal/app"
	"cloud-clipboard/internal/store"
	"cloud-clipboard/version"
)

func NewRouter(app *appstate.App, staticFiles fs.FS, uploadSessions *store.UploadSessionStore) (http.Handler, error) {
	sessions := NewSessionStore()
	adminHandler := NewAdminHandler(app.Admin, sessions)
	roomHandler := NewRoomHandler(app.Room, app.Settings, app.Files, uploadSessions, app.Config)
	adminRoomHandler := NewAdminRoomHandler(app.Room, app.Statistics)
	settingsHandler := NewSettingsHandler(app.Settings)
	fileHandler := NewFileHandler(app.Room, app.Files)
	syncHandler := NewSyncHandler(app.Hub, app.Room, sessions)

	rawIndex, err := fs.ReadFile(staticFiles, "index.html")
	if err != nil {
		return nil, err
	}
	indexHTML := strings.ReplaceAll(string(rawIndex), "{{BUILD}}", version.Short())
	indexHTML = strings.ReplaceAll(indexHTML, "{{API_VERSION}}", strconv.Itoa(APIVersion))

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})

	mux.HandleFunc("/api/bootstrap/status", adminHandler.BootstrapStatus)
	mux.HandleFunc("/api/bootstrap/settings", settingsHandler.Public)
	mux.HandleFunc("/api/admin/login", adminHandler.Login)
	mux.HandleFunc("/api/admin/logout", adminHandler.Logout)
	mux.HandleFunc("/api/admin/session", adminHandler.Session)
	mux.HandleFunc("/api/admin/password", sessions.Require(adminHandler.ChangePassword))
	mux.HandleFunc("/api/admin/settings", sessions.Require(settingsHandler.Manage))
	mux.HandleFunc("/api/admin/rooms", sessions.Require(adminRoomHandler.Manage))
	mux.HandleFunc("/api/admin/rooms/", sessions.Require(adminRoomHandler.Manage))

	// Room creation lives on the exact path; the former public room listing is
	// intentionally gone so active rooms cannot be enumerated.
	mux.HandleFunc("/api/rooms", roomHandler.ListOrCreate)
	mux.HandleFunc("/api/rooms/", func(w http.ResponseWriter, r *http.Request) {
		routeRoomPath(roomHandler, fileHandler, w, r)
	})

	// Legacy globally-scoped message endpoints are removed. Returning 404 for
	// the whole prefix avoids leaking which message IDs exist.
	mux.HandleFunc("/api/messages/", func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})

	mux.HandleFunc("/ws/rooms/", syncHandler.RoomWebSocket)

	staticServer := http.FileServer(http.FS(staticFiles))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/", "/index.html":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-cache")
			_, _ = w.Write([]byte(indexHTML))
			return
		}
		if isStaticAsset(r.URL.Path) {
			if r.URL.Query().Get("v") != "" {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			} else {
				w.Header().Set("Cache-Control", "public, max-age=3600")
			}
		}
		staticServer.ServeHTTP(w, r)
	})

	return withSecurityHeaders(mux), nil
}

// routeRoomPath dispatches /api/rooms/{code}/... routes. Every message, file
// and upload operation is scoped to a room code so global IDs can no longer
// reach another room's data.
func routeRoomPath(roomHandler *RoomHandler, fileHandler *FileHandler, w http.ResponseWriter, r *http.Request) {
	parts := splitPath(r.URL.Path, "/api/rooms/")
	if len(parts) == 0 {
		http.NotFound(w, r)
		return
	}
	roomCode := strings.ToUpper(parts[0])

	if len(parts) == 1 {
		switch r.Method {
		case http.MethodGet:
			roomHandler.RoomDetail(w, r, roomCode)
		case http.MethodDelete:
			roomHandler.PublicDeleteRoom(w, r, roomCode)
		default:
			errorJSON(w, http.StatusMethodNotAllowed, "method not allowed")
		}
		return
	}

	switch parts[1] {
	case "detail":
		if len(parts) == 2 {
			roomHandler.RoomDetail(w, r, roomCode)
			return
		}
	case "messages":
		switch {
		case len(parts) == 2:
			roomHandler.Messages(w, r, roomCode)
			return
		case len(parts) == 3:
			if messageID, ok := parsePathID(parts[2]); ok {
				roomHandler.DeleteMessage(w, r, roomCode, messageID)
				return
			}
			errorJSON(w, http.StatusBadRequest, "invalid message id")
			return
		case len(parts) == 4 && parts[3] == "pin":
			if messageID, ok := parsePathID(parts[2]); ok {
				roomHandler.TogglePinMessage(w, r, roomCode, messageID)
				return
			}
			errorJSON(w, http.StatusBadRequest, "invalid message id")
			return
		case len(parts) == 4 && parts[3] == "file":
			if messageID, ok := parsePathID(parts[2]); ok {
				fileHandler.Download(w, r, roomCode, messageID)
				return
			}
			errorJSON(w, http.StatusBadRequest, "invalid message id")
			return
		}
	case "events":
		if len(parts) == 2 {
			roomHandler.Events(w, r, roomCode)
			return
		}
	case "files":
		if len(parts) == 2 {
			roomHandler.UploadFile(w, r, roomCode)
			return
		}
	case "uploads":
		if len(parts) == 2 {
			roomHandler.CreateUploadSession(w, r, roomCode)
			return
		}
		if len(parts) == 4 && parts[3] == "chunk" {
			roomHandler.UploadChunk(w, r, roomCode, parts[2])
			return
		}
		if len(parts) == 4 && parts[3] == "complete" {
			roomHandler.FinalizeUpload(w, r, roomCode, parts[2])
			return
		}
	case "extend":
		if len(parts) == 2 {
			roomHandler.ExtendRoom(w, r, roomCode)
			return
		}
	case "permanent":
		if len(parts) == 2 {
			roomHandler.SetPermanentRoom(w, r, roomCode)
			return
		}
	case "name":
		if len(parts) == 2 {
			roomHandler.UpdateRoomName(w, r, roomCode)
			return
		}
	}

	http.NotFound(w, r)
}

func splitPath(path string, prefix string) []string {
	rest := strings.Trim(strings.TrimPrefix(path, prefix), "/")
	if rest == "" {
		return nil
	}
	parts := strings.Split(rest, "/")
	for _, part := range parts {
		if part == "" {
			return nil
		}
	}
	return parts
}

func parsePathID(raw string) (int64, bool) {
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

func isStaticAsset(path string) bool {
	for _, prefix := range []string{"/css/", "/js/", "/icon/"} {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return path == "/manifest.webmanifest"
}
