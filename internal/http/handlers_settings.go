package httpapi

import (
	"net/http"

	"cloud-clipboard/internal/service"
)

type SettingsHandler struct {
	settings *service.SettingsService
}

func NewSettingsHandler(settings *service.SettingsService) *SettingsHandler {
	return &SettingsHandler{settings: settings}
}

func (h *SettingsHandler) Manage(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		values, err := h.settings.GetAll()
		if err != nil {
			errorJSON(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, values)
	case http.MethodPut:
		var req service.SettingsPayload
		if err := readJSON(r, &req); err != nil {
			errorJSON(w, http.StatusBadRequest, "invalid request body")
			return
		}
		if err := h.settings.Update(req, nowUTC()); err != nil {
			errorJSON(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	default:
		errorJSON(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *SettingsHandler) Public(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		errorJSON(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	values, err := h.settings.GetPublic()
	if err != nil {
		errorJSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, values)
}
