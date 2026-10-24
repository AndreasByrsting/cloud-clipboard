package httpapi

// APIVersion is bumped whenever the HTTP API contract changes in a way that
// requires the browser bundle to be refreshed. The version is exposed via
// /api/bootstrap/status and injected into index.html so stale clients can
// detect a mismatch and reload.
const APIVersion = 2
