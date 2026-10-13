package handler

import "net/http"

// limitRequestBody applies a hard limit before a handler parses an upload. The
// Content-Length check rejects oversized declared bodies without reading them;
// MaxBytesReader also enforces the same bound for streamed/chunked requests.
func limitRequestBody(w http.ResponseWriter, r *http.Request, maxBytes int64) bool {
	if r.ContentLength > maxBytes {
		http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	return true
}
