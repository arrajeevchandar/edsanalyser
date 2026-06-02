package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"edsanalyser/internal/scanner"
)

type Server struct {
	service   *scanner.Service
	staticDir string
}

func NewServer(service *scanner.Service, staticDir string) http.Handler {
	server := &Server{service: service, staticDir: staticDir}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", server.health)
	mux.HandleFunc("/api/scans", server.scans)
	mux.HandleFunc("/api/scans/", server.scanByID)
	mux.HandleFunc("/", server.static)
	return withCORS(mux)
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) scans(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		scans, err := s.service.ListScans()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, scans)
	case http.MethodPost:
		var body struct {
			URL             string `json:"url"`
			AuditLimit      *int   `json:"auditLimit"`
			LighthouseMode  string `json:"lighthouseMode"`
			LighthouseLimit *int   `json:"lighthouseLimit"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		opts := scanner.DefaultScanOptions()
		opts.CrawlLimit = body.AuditLimit
		if body.LighthouseMode != "" {
			opts.LighthouseMode = body.LighthouseMode
		}
		if body.LighthouseLimit != nil {
			opts.LighthouseLimit = *body.LighthouseLimit
		}
		scan, err := s.service.StartScan(r.Context(), body.URL, opts)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusCreated, scan)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *Server) scanByID(w http.ResponseWriter, r *http.Request) {
	trimmed := strings.TrimPrefix(r.URL.Path, "/api/scans/")
	parts := strings.Split(strings.Trim(trimmed, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, http.StatusNotFound, errors.New("scan id is required"))
		return
	}
	id := parts[0]

	if len(parts) == 1 && r.Method == http.MethodGet {
		result, err := s.service.GetScan(id)
		if err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, sql.ErrNoRows) {
				status = http.StatusNotFound
			}
			writeError(w, status, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
		return
	}

	if len(parts) == 2 && parts[1] == "events" && r.Method == http.MethodGet {
		s.events(w, r, id)
		return
	}

	if len(parts) == 2 && parts[1] == "cancel" && r.Method == http.MethodPost {
		if err := s.service.CancelScan(id); err != nil {
			writeError(w, http.StatusConflict, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "cancelling"})
		return
	}

	if len(parts) == 2 && parts[1] == "audit" && r.Method == http.MethodPost {
		var body struct {
			Mode  string `json:"mode"`
			Limit *int   `json:"limit"`
		}
		// Body is optional; default to auditing every page.
		_ = json.NewDecoder(r.Body).Decode(&body)
		opts := scanner.ScanOptions{LighthouseMode: "all"}
		if body.Mode != "" {
			opts.LighthouseMode = body.Mode
		}
		if body.Limit != nil {
			opts.LighthouseLimit = *body.Limit
		}
		scan, err := s.service.ReauditScan(id, opts)
		if err != nil {
			status := http.StatusConflict
			if errors.Is(err, sql.ErrNoRows) {
				status = http.StatusNotFound
			}
			writeError(w, status, err)
			return
		}
		writeJSON(w, http.StatusOK, scan)
		return
	}

	w.WriteHeader(http.StatusNotFound)
}

func (s *Server) events(w http.ResponseWriter, r *http.Request, scanID string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, errors.New("streaming unsupported"))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	events, unsubscribe := s.service.Subscribe(scanID)
	defer unsubscribe()
	fmt.Fprintf(w, "event: open\ndata: %s\n\n", strconv.Quote(scanID))
	flusher.Flush()

	for {
		select {
		case event, ok := <-events:
			if !ok {
				return
			}
			payload, _ := json.Marshal(event)
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.Type, payload)
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

func (s *Server) static(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	index := filepath.Join(s.staticDir, "index.html")
	if _, err := os.Stat(index); err != nil {
		writeJSON(w, http.StatusOK, map[string]string{
			"message": "EDS Analyser API is running. Build the React app with npm.cmd run build to serve the dashboard from Go.",
		})
		return
	}
	path := filepath.Join(s.staticDir, filepath.Clean(r.URL.Path))
	if info, err := os.Stat(path); err == nil && !info.IsDir() {
		http.ServeFile(w, r, path)
		return
	}
	http.ServeFile(w, r, index)
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}
