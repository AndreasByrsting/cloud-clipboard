package httpapi

import (
	"errors"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"

	"cloud-clipboard/internal/service"
	"cloud-clipboard/internal/store"
)

type FileHandler struct {
	rooms *service.RoomService
	files *service.FileService
}

func NewFileHandler(rooms *service.RoomService, files *service.FileService) *FileHandler {
	return &FileHandler{rooms: rooms, files: files}
}

func (h *FileHandler) Download(w http.ResponseWriter, r *http.Request, roomCode string, messageID int64) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		errorJSON(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	message, err := h.rooms.GetFileMessage(roomCode, messageID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			errorJSON(w, http.StatusNotFound, "文件不存在或已过期")
			return
		}
		errorJSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	if strings.TrimSpace(message.FilePath) == "" {
		errorJSON(w, http.StatusNotFound, "文件不存在或已过期")
		return
	}

	file, info, err := h.files.Open(message.FilePath)
	if err != nil {
		errorJSON(w, http.StatusNotFound, "文件不存在或已过期")
		return
	}
	defer file.Close()

	mimeType := strings.TrimSpace(message.MimeType)
	if mimeType == "" {
		mimeType = mime.TypeByExtension(filepath.Ext(message.FilePath))
	}
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	// net/http's mime table may append "; charset=utf-8"; normalize before the
	// dangerous/inline checks so text/html and image/svg+xml are recognized.
	mimeType = normalizeMimeType(mimeType)
	// Never let an uploaded SVG/HTML/XML execute in the application origin.
	if isDangerousFileType(mimeType) {
		mimeType = "application/octet-stream"
	}
	inline := r.URL.Query().Get("inline") == "1" && isInlineImage(mimeType)

	fileName := strings.TrimSpace(message.FileName)
	if fileName == "" {
		fileName = "download"
	}
	w.Header().Set("Content-Type", mimeType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "sandbox")
	w.Header().Set("Cache-Control", "private, max-age=3600")
	w.Header().Set("Content-Disposition", contentDisposition(fileName, inline))
	http.ServeContent(w, r, fileName, info.ModTime(), file)
}

func normalizeMimeType(raw string) string {
	base, _, _ := strings.Cut(strings.ToLower(strings.TrimSpace(raw)), ";")
	base = strings.TrimSpace(base)
	if base == "" {
		return "application/octet-stream"
	}
	return base
}

func isDangerousFileType(mimeType string) bool {
	switch strings.ToLower(mimeType) {
	case "image/svg+xml",
		"text/html",
		"application/xhtml+xml",
		"text/xml",
		"application/xml",
		"application/javascript",
		"text/javascript":
		return true
	default:
		return false
	}
}

func isInlineImage(mimeType string) bool {
	switch strings.ToLower(mimeType) {
	case "image/png", "image/jpeg", "image/gif", "image/webp", "image/avif":
		return true
	default:
		return false
	}
}

func contentDisposition(name string, inline bool) string {
	disposition := "attachment"
	if inline {
		disposition = "inline"
	}
	fallback := asciiDownloadName(name)
	encoded := strings.ReplaceAll(url.QueryEscape(name), "+", "%20")
	return fmt.Sprintf(`%s; filename="%s"; filename*=UTF-8''%s`, disposition, fallback, encoded)
}

func asciiDownloadName(name string) string {
	var builder strings.Builder
	for _, r := range name {
		switch {
		case r > 127, r < 32, r == 127:
			builder.WriteByte('_')
		case r == '"' || r == '\\' || r == ';':
			builder.WriteByte('_')
		default:
			builder.WriteRune(r)
		}
	}
	cleaned := strings.TrimSpace(builder.String())
	if cleaned == "" {
		return "download"
	}
	if len(cleaned) > 120 {
		cleaned = cleaned[:120]
	}
	return cleaned
}
