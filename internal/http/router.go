package httpapi

import (
	"io/fs"
	"net/http"
	"strings"

	appstate "cloud-clipboard/internal/app"
	"cloud-clipboard/internal/store"
)

func NewRouter(app *appstate.App, staticFiles fs.FS, uploadSessions *store.UploadSessionStore) (http.Handler, error) {
	sessions := NewSessionStore()
	adminHandler := NewAdminHandler(app.Admin, sessions)
	roomHandler := NewRoomHandler(app.Room, app.Settings, app.Files, uploadSessions, app.Config)
	adminRoomHandler := NewAdminRoomHandler(app.Room, app.Statistics)
	settingsHandler := NewSettingsHandler(app.Settings)
	fileHandler := NewFileHandler(app.Room, app.Files)
	syncHandler := NewSyncHandler(app.Hub, app.Room, sessions)

	staticFS := staticFiles

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

	mux.HandleFunc("/api/rooms", roomHandler.ListOrCreate)
	mux.HandleFunc("/api/rooms/", func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/messages"):
			roomHandler.Messages(w, r)
		case strings.HasSuffix(r.URL.Path, "/events"):
			roomHandler.Events(w, r)
		case strings.HasSuffix(r.URL.Path, "/files"):
			roomHandler.UploadFile(w, r)
		case strings.HasSuffix(r.URL.Path, "/uploads"):
			roomHandler.CreateUploadSession(w, r)
		case strings.HasSuffix(r.URL.Path, "/extend"):
			roomHandler.ExtendRoom(w, r)
		case strings.HasSuffix(r.URL.Path, "/permanent"):
			roomHandler.SetPermanentRoom(w, r)
		case strings.HasSuffix(r.URL.Path, "/detail"):
			roomHandler.RoomDetail(w, r)
		case strings.HasSuffix(r.URL.Path, "/name"):
			roomHandler.UpdateRoomName(w, r)
		default:
			parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
			if len(parts) == 3 && r.Method == http.MethodDelete {
				roomHandler.PublicDeleteRoom(w, r)
				return
			}
			http.NotFound(w, r)
		}
	})

	mux.HandleFunc("/api/messages/", func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/file"):
			fileHandler.Download(w, r)
		case strings.HasSuffix(r.URL.Path, "/pin"):
			roomHandler.TogglePinMessage(w, r)
		case strings.HasPrefix(r.URL.Path, "/api/messages/uploads/"):
			if strings.HasSuffix(r.URL.Path, "/complete") {
				roomHandler.FinalizeUpload(w, r)
			} else if strings.HasSuffix(r.URL.Path, "/chunk") {
				roomHandler.UploadChunk(w, r)
			} else {
				http.NotFound(w, r)
			}
		default:
			parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
			if len(parts) == 3 && r.Method == http.MethodDelete {
				roomHandler.DeleteMessage(w, r)
				return
			}
			http.NotFound(w, r)
		}
	})

	mux.HandleFunc("/ws/rooms/", syncHandler.RoomWebSocket)
	mux.Handle("/", http.FileServer(http.FS(staticFS)))
	return mux, nil
}
