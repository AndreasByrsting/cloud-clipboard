package httpapi

import (
	"encoding/json"
	"net/http"
)

// maxJSONBodyBytes caps decoder input for every JSON API request. Endpoints
// with their own limits (message length, upload size) enforce them separately.
const maxJSONBodyBytes int64 = 1 << 20

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func readJSON(r *http.Request, dst any) error {
	defer r.Body.Close()
	// Passing a nil ResponseWriter is supported by net/http: the body is still
	// limited, and oversized requests fail with "request body too large".
	r.Body = http.MaxBytesReader(nil, r.Body, maxJSONBodyBytes)
	return json.NewDecoder(r.Body).Decode(dst)
}

func errorJSON(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
