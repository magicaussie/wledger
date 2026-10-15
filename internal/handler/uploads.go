package handler

import (
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/tuxedocurly/wledger/internal/config"
)

// HandleUploads serves user-uploaded assets (part images and documents).
//
// It replaces a bare http.FileServer for three reasons:
//
//  1. Uploaded content must obey the same read-authorization policy as the
//     rest of the application. A bare FileServer route registered before the
//     read-auth group left every upload publicly reachable even when
//     require_auth_for_read was enabled.
//  2. Directory listings must never be produced; the FileServer happily
//     rendered index-less directories, exposing stored filenames.
//  3. Documents preserve their original extension and bytes, so an uploaded
//     HTML/SVG file served inline would execute script in the application
//     origin. Documents are therefore always returned as downloads with a
//     non-renderable content type, and no uploaded file is sniffed into an
//     executable type.
func (h *Handler) HandleUploads(w http.ResponseWriter, r *http.Request) {
	rel := strings.TrimPrefix(r.URL.Path, config.UrlPrefixUploads)
	cleaned := path.Clean("/" + rel)

	// Never serve a directory or a directory listing.
	if cleaned == "/" || strings.HasSuffix(r.URL.Path, "/") {
		http.NotFound(w, r)
		return
	}

	diskPath := filepath.Join(config.DirUploads, filepath.FromSlash(cleaned))

	// Never serve a hidden entry. The uploads tree holds a hidden restore control
	// plane (the in-mount generation staging directory and the durable restore
	// journal) that must never be reachable over HTTP, and no legitimate uploaded
	// asset begins with a dot.
	for _, segment := range strings.Split(strings.TrimPrefix(cleaned, "/"), "/") {
		if strings.HasPrefix(segment, ".") {
			http.NotFound(w, r)
			return
		}
	}

	info, err := os.Stat(diskPath)
	if err != nil || info.IsDir() {
		http.NotFound(w, r)
		return
	}

	// Uploaded content must never be sniffed into an executable type.
	w.Header().Set("X-Content-Type-Options", "nosniff")

	if strings.HasPrefix(cleaned, "/docs/") {
		w.Header().Set("Content-Disposition", "attachment")
		w.Header().Set("Content-Type", "application/octet-stream")
	} else {
		// Images are re-encoded to raster JPEG on upload, but keep a
		// restrictive policy header as defense in depth.
		w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	}

	http.ServeFile(w, r, diskPath)
}
