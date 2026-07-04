package httpapi

import (
	"net/http"
	"strings"

	"cloud-clipboard/internal/service"
)

type FileHandler struct {
	rooms *service.RoomService
	files *service.FileService
}

func NewFileHandler(rooms *service.RoomService, files *service.FileService) *FileHandler {
	return &FileHandler{rooms: rooms, files: files}
}

func (h *FileHandler) Download(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		errorJSON(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	messageID, err := messageIDFromPath(r.URL.Path)
	if err != nil {
		errorJSON(w, http.StatusBadRequest, err.Error())
		return
	}
	message, err := h.rooms.GetFileMessage(messageID)
	if err != nil {
		errorJSON(w, http.StatusNotFound, err.Error())
		return
	}
	content, mimeType, err := h.files.Read(message.FilePath)
	if err != nil {
		errorJSON(w, http.StatusNotFound, err.Error())
		return
	}
	w.Header().Set("Content-Type", mimeType)
	disposition := "attachment"
	if strings.HasPrefix(mimeType, "image/") {
		disposition = "inline"
	}
	w.Header().Set("Content-Disposition", disposition+`; filename="`+message.FileName+`"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(content)
}
