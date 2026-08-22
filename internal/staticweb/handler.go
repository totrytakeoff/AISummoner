// Package staticweb serves the embedded production WebUI with a constrained
// single-page-application fallback.
package staticweb

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	indexFile       = "index.html"
	immutableAssets = "public, max-age=31536000, immutable"
	noCache         = "no-cache"
)

//go:embed assets
var embeddedFiles embed.FS

type Handler struct {
	files fs.FS
}

// NewEmbedded returns the handler backed by the assets compiled into the
// Server binary. A tracked placeholder keeps clean Go checkouts buildable;
// the production Docker build replaces it with web/dist before compilation.
func NewEmbedded() (*Handler, error) {
	assets, err := fs.Sub(embeddedFiles, "assets")
	if err != nil {
		return nil, fmt.Errorf("open embedded Web assets: %w", err)
	}
	return New(assets)
}

// New constructs a handler from an injected filesystem. The root index must
// exist because every valid client-side route falls back to that exact file.
func New(files fs.FS) (*Handler, error) {
	if files == nil {
		return nil, errors.New("static Web filesystem is required")
	}
	info, err := fs.Stat(files, indexFile)
	if err != nil || !info.Mode().IsRegular() {
		return nil, errors.New("static Web index.html is required")
	}
	return &Handler{files: files}, nil
}

func (handler *Handler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		writer.Header().Set("Allow", http.MethodGet+", "+http.MethodHead)
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	requestPath := request.URL.Path
	if !safeRequestPath(requestPath) || reservedPublicPath(requestPath) {
		http.NotFound(writer, request)
		return
	}
	if requestPath == "/assets" {
		http.NotFound(writer, request)
		return
	}

	name := strings.TrimPrefix(requestPath, "/")
	if name == "" {
		name = indexFile
	}
	info, err := fs.Stat(handler.files, name)
	if err == nil && info.Mode().IsRegular() {
		handler.serveFile(writer, request, name)
		return
	}

	// Asset/file misses are real misses. Only extensionless browser routes use
	// the SPA shell, so API typos and broken production assets cannot look OK.
	if strings.HasPrefix(requestPath, "/assets/") || path.Ext(name) != "" {
		http.NotFound(writer, request)
		return
	}
	handler.serveFile(writer, request, indexFile)
}

func (handler *Handler) serveFile(writer http.ResponseWriter, request *http.Request, name string) {
	content, err := fs.ReadFile(handler.files, name)
	if err != nil {
		http.NotFound(writer, request)
		return
	}
	contentType := mime.TypeByExtension(path.Ext(name))
	if contentType == "" {
		contentType = http.DetectContentType(content)
	}
	writer.Header().Set("Content-Type", contentType)
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	if hashedAsset(name) {
		writer.Header().Set("Cache-Control", immutableAssets)
	} else {
		writer.Header().Set("Cache-Control", noCache)
	}
	http.ServeContent(writer, request, path.Base(name), time.Time{}, bytes.NewReader(content))
}

func safeRequestPath(value string) bool {
	if value == "" || value[0] != '/' || !utf8.ValidString(value) ||
		strings.ContainsRune(value, '\x00') || strings.ContainsRune(value, '\\') {
		return false
	}
	trimmed := strings.TrimPrefix(value, "/")
	if strings.HasPrefix(trimmed, "/") || strings.Contains(trimmed, "//") {
		return false
	}
	for _, segment := range strings.Split(trimmed, "/") {
		if segment == "." || segment == ".." {
			return false
		}
	}
	return true
}

func reservedPublicPath(value string) bool {
	for _, prefix := range []string{"/api", "/internal", "/healthz"} {
		if value == prefix || strings.HasPrefix(value, prefix+"/") {
			return true
		}
	}
	return false
}

func hashedAsset(name string) bool {
	if !strings.HasPrefix(name, "assets/") || strings.Contains(strings.TrimPrefix(name, "assets/"), "/") {
		return false
	}
	base := strings.TrimSuffix(path.Base(name), path.Ext(name))
	separator := strings.LastIndexByte(base, '-')
	if separator < 0 || len(base)-separator-1 < 8 {
		return false
	}
	for _, character := range base[separator+1:] {
		if (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') &&
			(character < '0' || character > '9') && character != '_' {
			return false
		}
	}
	return true
}
