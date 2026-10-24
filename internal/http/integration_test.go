package httpapi

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	appstate "cloud-clipboard/internal/app"
	"cloud-clipboard/internal/config"
	"cloud-clipboard/internal/db"
	"cloud-clipboard/internal/realtime"
	"cloud-clipboard/internal/service"
	"cloud-clipboard/internal/store"
)

type testResponse struct {
	status int
	header http.Header
	body   []byte
}

func newTestServer(t *testing.T) (*httptest.Server, *store.SettingsStore) {
	t.Helper()

	originalLocation := config.TimeLocation
	config.TimeLocation = time.UTC
	t.Cleanup(func() { config.TimeLocation = originalLocation })

	dir := t.TempDir()
	database, err := db.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	schema, err := os.ReadFile(filepath.Join("..", "..", "sql", "init.sql"))
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	if err := db.Migrate(database, string(schema)); err != nil {
		t.Fatalf("migrate test database: %v", err)
	}

	now := time.Now().In(config.TimeLocation)
	settingsStore := store.NewSettingsStore(database)
	if err := settingsStore.EnsureDefaults(now); err != nil {
		t.Fatalf("ensure defaults: %v", err)
	}

	adminStore := store.NewAdminStore(database)
	adminService := service.NewAdminService(adminStore)
	if err := adminService.EnsureInitialized("", now); err != nil {
		t.Fatalf("initialize admin: %v", err)
	}

	roomStore := store.NewRoomStore(database)
	messageStore := store.NewMessageStore(database)
	uploadStore := store.NewUploadSessionStore(database)
	statisticsStore := store.NewStatisticsStore(database)
	fileService := service.NewFileService(filepath.Join(dir, "uploads"))
	hub := realtime.NewHub()

	roomService := service.NewRoomService(settingsStore, roomStore, messageStore, uploadStore, fileService, hub)
	settingsService := service.NewSettingsService(settingsStore)
	maintenanceService := service.NewMaintenanceService(settingsStore, roomStore, messageStore, fileService, roomService, statisticsStore, uploadStore)

	application := &appstate.App{
		Config:      config.Config{MaxUploadBytes: 1 << 20},
		DB:          database,
		Hub:         hub,
		Admin:       adminService,
		Room:        roomService,
		Settings:    settingsService,
		Files:       fileService,
		Maintenance: maintenanceService,
		Statistics:  statisticsStore,
	}

	staticFS := fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<!doctype html><html><head><script>window.__APP_BUILD__='{{BUILD}}';window.__API_VERSION__='{{API_VERSION}}';</script></head><body></body></html>")},
		"js/app.js":  &fstest.MapFile{Data: []byte("// test bundle\n")},
	}
	handler, err := NewRouter(application, staticFS, uploadStore)
	if err != nil {
		t.Fatalf("build router: %v", err)
	}

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return server, settingsStore
}

func doRequest(t *testing.T, client *http.Client, method string, url string, body []byte, contentType string) testResponse {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	} else {
		reader = bytes.NewReader(nil)
	}
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(resp.Body); err != nil {
		t.Fatalf("read body: %v", err)
	}
	return testResponse{status: resp.StatusCode, header: resp.Header, body: buf.Bytes()}
}

func doJSON(t *testing.T, client *http.Client, method string, url string, payload any) testResponse {
	t.Helper()
	var body []byte
	if payload != nil {
		var err error
		body, err = json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal payload: %v", err)
		}
	}
	return doRequest(t, client, method, url, body, "application/json")
}

func decodeObject(t *testing.T, resp testResponse) map[string]any {
	t.Helper()
	var data map[string]any
	if err := json.Unmarshal(resp.body, &data); err != nil {
		t.Fatalf("decode response (status %d, body %q): %v", resp.status, string(resp.body), err)
	}
	return data
}

func createRoom(t *testing.T, server *httptest.Server) string {
	t.Helper()
	resp := doJSON(t, http.DefaultClient, http.MethodPost, server.URL+"/api/rooms", nil)
	if resp.status != http.StatusCreated {
		t.Fatalf("create room status = %d, body = %s", resp.status, string(resp.body))
	}
	code, _ := decodeObject(t, resp)["roomCode"].(string)
	if code == "" {
		t.Fatalf("create room returned no roomCode: %s", string(resp.body))
	}
	return code
}

func postTextMessage(t *testing.T, server *httptest.Server, roomCode string, text string) int64 {
	t.Helper()
	resp := doJSON(t, http.DefaultClient, http.MethodPost, server.URL+"/api/rooms/"+roomCode+"/messages", map[string]string{"text": text})
	if resp.status != http.StatusCreated {
		t.Fatalf("post message status = %d, body = %s", resp.status, string(resp.body))
	}
	data := decodeObject(t, resp)
	message, _ := data["message"].(map[string]any)
	id, _ := message["id"].(float64)
	if id <= 0 {
		t.Fatalf("post message returned no id: %s", string(resp.body))
	}
	return int64(id)
}

func TestRoomScopedAuthorization(t *testing.T) {
	server, _ := newTestServer(t)
	client := &http.Client{}
	roomA := createRoom(t, server)
	roomB := createRoom(t, server)
	messageID := postTextMessage(t, server, roomA, "hello")

	cases := []struct {
		name   string
		method string
		path   string
		want   int
	}{
		{name: "room listing removed", method: http.MethodGet, path: "/api/rooms", want: http.StatusMethodNotAllowed},
		{name: "legacy global file", method: http.MethodGet, path: "/api/messages/1/file", want: http.StatusNotFound},
		{name: "legacy global delete", method: http.MethodDelete, path: "/api/messages/1", want: http.StatusNotFound},
		{name: "cross-room file", method: http.MethodGet, path: "/api/rooms/" + roomB + "/messages/1/file", want: http.StatusNotFound},
		{name: "cross-room delete", method: http.MethodDelete, path: "/api/rooms/" + roomB + "/messages/1", want: http.StatusNotFound},
		{name: "cross-room pin", method: http.MethodPost, path: "/api/rooms/" + roomB + "/messages/1/pin", want: http.StatusNotFound},
		{name: "same-room pin", method: http.MethodPost, path: "/api/rooms/" + roomA + "/messages/1/pin", want: http.StatusOK},
		{name: "same-room delete", method: http.MethodDelete, path: "/api/rooms/" + roomA + "/messages/1", want: http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Use literal IDs above; replace with the actual message ID where the
			// case targets the message that was just created.
			path := tc.path
			if strings.Contains(path, "/messages/1") {
				path = strings.Replace(path, "/messages/1", "/messages/"+itoa(messageID), 1)
			}
			resp := doRequest(t, client, tc.method, server.URL+path, nil, "")
			if resp.status != tc.want {
				t.Fatalf("%s %s = %d, want %d (body %s)", tc.method, path, resp.status, tc.want, string(resp.body))
			}
		})
	}
}

func itoa(value int64) string {
	if value == 0 {
		return "0"
	}
	var digits [20]byte
	pos := len(digits)
	for value > 0 {
		pos--
		digits[pos] = byte('0' + value%10)
		value /= 10
	}
	return string(digits[pos:])
}

func TestUploadLimitsAndDownloadHardening(t *testing.T) {
	server, settingsStore := newTestServer(t)
	client := &http.Client{}
	roomCode := createRoom(t, server)

	if err := settingsStore.SetInt("max_upload_bytes", 1024, time.Now()); err != nil {
		t.Fatalf("set upload limit: %v", err)
	}

	resp := doJSON(t, client, http.MethodPost, server.URL+"/api/rooms/"+roomCode+"/uploads", map[string]any{
		"fileName": "too-large.bin",
		"fileSize": 2048,
	})
	if resp.status != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversize upload session = %d, want 413 (body %s)", resp.status, string(resp.body))
	}

	missingRoom := doJSON(t, client, http.MethodPost, server.URL+"/api/rooms/NO_SUCH_ROOM/uploads", map[string]any{
		"fileName": "file.bin",
		"fileSize": 10,
	})
	if missingRoom.status != http.StatusNotFound {
		t.Fatalf("upload session for missing room = %d, want 404 (body %s)", missingRoom.status, string(missingRoom.body))
	}

	// Multipart upload of an SVG must be served as a download, never inline.
	var multipartBody bytes.Buffer
	writer := multipart.NewWriter(&multipartBody)
	part, err := writer.CreateFormFile("file", "evil.svg")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := part.Write([]byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`)); err != nil {
		t.Fatalf("write form file: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	uploadResp := doRequest(t, client, http.MethodPost, server.URL+"/api/rooms/"+roomCode+"/files", multipartBody.Bytes(), writer.FormDataContentType())
	if uploadResp.status != http.StatusCreated {
		t.Fatalf("multipart upload = %d, want 201 (body %s)", uploadResp.status, string(uploadResp.body))
	}
	uploaded := decodeObject(t, uploadResp)
	message, _ := uploaded["message"].(map[string]any)
	fileID := int64(message["id"].(float64))

	download := doRequest(t, client, http.MethodGet, server.URL+"/api/rooms/"+roomCode+"/messages/"+itoa(fileID)+"/file?inline=1", nil, "")
	if download.status != http.StatusOK {
		t.Fatalf("download = %d, want 200 (body %s)", download.status, string(download.body))
	}
	if got := download.header.Get("Content-Type"); got != "application/octet-stream" {
		t.Fatalf("Content-Type = %q, want application/octet-stream", got)
	}
	if disposition := download.header.Get("Content-Disposition"); !strings.HasPrefix(disposition, "attachment") {
		t.Fatalf("Content-Disposition = %q, want attachment", disposition)
	}
	if got := download.header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q, want nosniff", got)
	}
	if got := download.header.Get("Content-Security-Policy"); !strings.Contains(got, "sandbox") {
		t.Fatalf("Content-Security-Policy = %q, want sandbox", got)
	}
	if !bytes.Contains(download.body, []byte("<svg")) {
		t.Fatal("download body should still contain the uploaded file content")
	}

	// Chunk upload must not exceed the declared file size.
	sessionResp := doJSON(t, client, http.MethodPost, server.URL+"/api/rooms/"+roomCode+"/uploads", map[string]any{
		"fileName": "small.bin",
		"fileSize": 10,
	})
	if sessionResp.status != http.StatusCreated {
		t.Fatalf("create session = %d (body %s)", sessionResp.status, string(sessionResp.body))
	}
	session := decodeObject(t, sessionResp)
	sessionID, _ := session["id"].(string)
	if sessionID == "" {
		t.Fatalf("session has no id: %s", string(sessionResp.body))
	}
	over := doRequest(t, client, http.MethodPut, server.URL+"/api/rooms/"+roomCode+"/uploads/"+sessionID+"/chunk", bytes.Repeat([]byte("x"), 11), "application/octet-stream")
	if over.status != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversize chunk = %d, want 413 (body %s)", over.status, string(over.body))
	}
	ok := doRequest(t, client, http.MethodPut, server.URL+"/api/rooms/"+roomCode+"/uploads/"+sessionID+"/chunk", bytes.Repeat([]byte("x"), 10), "application/octet-stream")
	if ok.status != http.StatusOK {
		t.Fatalf("exact chunk = %d, want 200 (body %s)", ok.status, string(ok.body))
	}
	finalize := doRequest(t, client, http.MethodPost, server.URL+"/api/rooms/"+roomCode+"/uploads/"+sessionID+"/complete", nil, "")
	if finalize.status != http.StatusOK {
		t.Fatalf("finalize = %d, want 200 (body %s)", finalize.status, string(finalize.body))
	}
}

func TestAdminPasswordChangeFlow(t *testing.T) {
	server, _ := newTestServer(t)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("create cookie jar: %v", err)
	}
	client := &http.Client{Jar: jar}

	login := doJSON(t, client, http.MethodPost, server.URL+"/api/admin/login", map[string]string{"password": config.DefaultAdminPassword})
	if login.status != http.StatusOK {
		t.Fatalf("default login = %d, want 200 (body %s)", login.status, string(login.body))
	}
	loginData := decodeObject(t, login)
	if enabled, _ := loginData["defaultPasswordActive"].(bool); !enabled {
		t.Fatal("default login should report defaultPasswordActive")
	}

	blocked := doRequest(t, client, http.MethodGet, server.URL+"/api/admin/rooms", nil, "")
	if blocked.status != http.StatusForbidden {
		t.Fatalf("admin rooms before password change = %d, want 403 (body %s)", blocked.status, string(blocked.body))
	}

	change := doJSON(t, client, http.MethodPost, server.URL+"/api/admin/password", map[string]string{
		"currentPassword": config.DefaultAdminPassword,
		"newPassword":     "new-password-123",
		"confirmPassword": "new-password-123",
	})
	if change.status != http.StatusOK {
		t.Fatalf("change password = %d, want 200 (body %s)", change.status, string(change.body))
	}

	oldSession := doRequest(t, client, http.MethodGet, server.URL+"/api/admin/rooms", nil, "")
	if oldSession.status != http.StatusUnauthorized {
		t.Fatalf("old session after password change = %d, want 401", oldSession.status)
	}

	relogin := doJSON(t, client, http.MethodPost, server.URL+"/api/admin/login", map[string]string{"password": "new-password-123"})
	if relogin.status != http.StatusOK {
		t.Fatalf("relogin = %d, want 200 (body %s)", relogin.status, string(relogin.body))
	}
	allowed := doRequest(t, client, http.MethodGet, server.URL+"/api/admin/rooms", nil, "")
	if allowed.status != http.StatusOK {
		t.Fatalf("admin rooms after relogin = %d, want 200 (body %s)", allowed.status, string(allowed.body))
	}
}

func TestVersionAndIndexInjection(t *testing.T) {
	server, _ := newTestServer(t)
	client := &http.Client{}

	index := doRequest(t, client, http.MethodGet, server.URL+"/", nil, "")
	if index.status != http.StatusOK {
		t.Fatalf("index status = %d", index.status)
	}
	if bytes.Contains(index.body, []byte("{{BUILD}}")) || bytes.Contains(index.body, []byte("{{API_VERSION}}")) {
		t.Fatalf("index still contains template placeholders: %s", string(index.body))
	}
	if !bytes.Contains(index.body, []byte(`window.__APP_BUILD__='dev'`)) {
		t.Fatalf("index missing injected build: %s", string(index.body))
	}
	if !bytes.Contains(index.body, []byte(`window.__API_VERSION__='2'`)) {
		t.Fatalf("index missing injected api version: %s", string(index.body))
	}

	status := doRequest(t, client, http.MethodGet, server.URL+"/api/bootstrap/status", nil, "")
	if status.status != http.StatusOK {
		t.Fatalf("bootstrap status = %d", status.status)
	}
	data := decodeObject(t, status)
	if _, ok := data["serverVersion"].(string); !ok {
		t.Fatalf("bootstrap status missing serverVersion: %s", string(status.body))
	}
	if _, ok := data["apiVersion"].(float64); !ok {
		t.Fatalf("bootstrap status missing apiVersion: %s", string(status.body))
	}
	if _, exists := data["defaultPasswordActive"]; exists {
		t.Fatalf("bootstrap status must not expose defaultPasswordActive: %s", string(status.body))
	}

	asset := doRequest(t, client, http.MethodGet, server.URL+"/js/app.js?v=test", nil, "")
	if asset.status != http.StatusOK {
		t.Fatalf("static asset status = %d", asset.status)
	}
	if cache := asset.header.Get("Cache-Control"); !strings.Contains(cache, "immutable") {
		t.Fatalf("versioned asset Cache-Control = %q, want immutable", cache)
	}
}
