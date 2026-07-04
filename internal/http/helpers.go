package httpapi

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"cloud-clipboard/internal/config"
	"cloud-clipboard/internal/service"
)

type AdminHandler struct {
	admin    *service.AdminService
	sessions *SessionStore
}

func NewAdminHandler(admin *service.AdminService, sessions *SessionStore) *AdminHandler {
	return &AdminHandler{admin: admin, sessions: sessions}
}

func (h *AdminHandler) BootstrapStatus(w http.ResponseWriter, r *http.Request) {
	cred, exists, err := h.admin.Status()
	if err != nil {
		errorJSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"adminExists":           exists,
		"defaultPasswordActive": cred.DefaultPasswordActive,
	})
}

func (h *AdminHandler) Session(w http.ResponseWriter, r *http.Request) {
	session, ok := h.sessions.Get(r)
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{"authenticated": false})
		return
	}
	cred, _, err := h.admin.Status()
	if err != nil {
		errorJSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"authenticated":         true,
		"defaultPasswordActive": cred.DefaultPasswordActive,
		"createdAt":             session.CreatedAt.Unix(),
		"lastSeenAt":            session.LastSeenAt.Unix(),
	})
}

func (h *AdminHandler) Login(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		errorJSON(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if err := readJSON(r, &req); err != nil {
		errorJSON(w, http.StatusBadRequest, "invalid request body")
		return
	}
	ok, cred, err := h.admin.Login(req.Password, nowUTC())
	if err != nil {
		errorJSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		errorJSON(w, http.StatusUnauthorized, "invalid password")
		return
	}
	token, err := service.GenerateToken()
	if err != nil {
		errorJSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	h.sessions.Create(token, w)
	writeJSON(w, http.StatusOK, map[string]any{
		"authenticated":         true,
		"defaultPasswordActive": cred.DefaultPasswordActive,
	})
}

func (h *AdminHandler) Logout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		errorJSON(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	h.sessions.Destroy(r, w)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (h *AdminHandler) ChangePassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		errorJSON(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req struct {
		NewPassword     string `json:"newPassword"`
		ConfirmPassword string `json:"confirmPassword"`
	}
	if err := readJSON(r, &req); err != nil {
		errorJSON(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if strings.TrimSpace(req.NewPassword) == "" || strings.TrimSpace(req.ConfirmPassword) == "" {
		errorJSON(w, http.StatusBadRequest, "password fields are required")
		return
	}
	if req.NewPassword != req.ConfirmPassword {
		errorJSON(w, http.StatusBadRequest, "password confirmation does not match")
		return
	}
	if err := h.admin.ChangePassword(req.NewPassword, nowUTC()); err != nil {
		errorJSON(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func roomCodeFromPath(path string) (string, error) {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 3 {
		return "", fmt.Errorf("invalid room path")
	}
	return parts[2], nil
}

func messageIDFromPath(path string) (int64, error) {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 3 {
		return 0, fmt.Errorf("invalid message path")
	}
	return strconv.ParseInt(parts[2], 10, 64)
}

func sinceEventID(r *http.Request) (int64, error) {
	raw := r.URL.Query().Get("since_event_id")
	if raw == "" {
		return 0, nil
	}
	return strconv.ParseInt(raw, 10, 64)
}

func nowUTC() time.Time {
	return time.Now().In(config.TimeLocation)
}
