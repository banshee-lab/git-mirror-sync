package dashboard

import (
	"context"
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"time"

	"forgejo-sync/internal/history"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static/*
var staticFS embed.FS

// HistoryReader defines the interface required by the dashboard to fetch recent events.
type HistoryReader interface {
	Recent(ctx context.Context, limit int) ([]history.SyncEvent, error)
}

// Handler holds dependencies and compiled templates for the dashboard web UI.
type Handler struct {
	historyStore HistoryReader
	tmpl         *template.Template
	logger       *slog.Logger
}

// Option configures a Handler.
type Option func(*Handler)

// WithLogger sets the logger for the dashboard handler.
func WithLogger(logger *slog.Logger) Option {
	return func(h *Handler) {
		h.logger = logger
	}
}

// templateFuncMap returns template helper functions for styling and formatting.
func templateFuncMap() template.FuncMap {
	return template.FuncMap{
		"statusBadgeClass": func(status history.Status) string {
			switch status {
			case history.StatusSuccess:
				return "bg-emerald-950/80 text-emerald-400 border-emerald-800/80"
			case history.StatusConflict:
				return "bg-amber-950/80 text-amber-400 border-amber-800/80"
			case history.StatusError:
				return "bg-rose-950/80 text-rose-400 border-rose-800/80"
			case history.StatusSkipped:
				return "bg-slate-800 text-slate-400 border-slate-700"
			case history.StatusReceived:
				return "bg-sky-950/80 text-sky-400 border-sky-800/80"
			default:
				return "bg-slate-800 text-slate-400 border-slate-700"
			}
		},
		"formatTime": func(t time.Time) string {
			if t.IsZero() {
				return "—"
			}
			return t.UTC().Format("2006-01-02 15:04:05 MST")
		},
		"formatDuration": func(d *int64) string {
			if d == nil {
				return "—"
			}
			if *d < 1000 {
				return fmt.Sprintf("%dms", *d)
			}
			return fmt.Sprintf("%.2fs", float64(*d)/1000.0)
		},
		"formatCommit": func(sha string) string {
			if sha == "" {
				return "—"
			}
			if len(sha) > 8 {
				return sha[:8]
			}
			return sha
		},
		"truncateDetail": func(detail string) string {
			const maxLen = 120
			if len(detail) > maxLen {
				return detail[:maxLen] + "..."
			}
			return detail
		},
	}
}

// NewHandler creates a new dashboard Handler with parsed templates and history store.
func NewHandler(historyStore HistoryReader, opts ...Option) (*Handler, error) {
	tmpl, err := template.New("base").Funcs(templateFuncMap()).ParseFS(templateFS, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("parse dashboard templates: %w", err)
	}

	h := &Handler{
		historyStore: historyStore,
		tmpl:         tmpl,
		logger:       slog.Default(),
	}

	for _, opt := range opts {
		opt(h)
	}

	return h, nil
}

// RegisterRoutes registers dashboard and health check routes onto the provided http.ServeMux.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) error {
	// Static files mounted at /static/
	subFS, err := fs.Sub(staticFS, "static")
	if err != nil {
		return fmt.Errorf("create static sub-filesystem: %w", err)
	}
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(subFS))))

	// Health check: returns 200 OK
	mux.HandleFunc("GET /healthz", h.HandleHealthz)

	// Dashboard root: shows last 30 sync events
	mux.HandleFunc("GET /", h.HandleIndex)

	return nil
}

// NewRouter creates and configures an http.ServeMux with all dashboard routes.
func NewRouter(historyStore HistoryReader, opts ...Option) (*http.ServeMux, error) {
	h, err := NewHandler(historyStore, opts...)
	if err != nil {
		return nil, err
	}

	mux := http.NewServeMux()
	if err := h.RegisterRoutes(mux); err != nil {
		return nil, err
	}

	return mux, nil
}

// HandleHealthz responds with HTTP 200 OK for container health checks.
func (h *Handler) HandleHealthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("OK\n"))
}

// IndexData holds view data for the index template.
type IndexData struct {
	Events []history.SyncEvent
}

// HandleIndex handles GET / and renders recent sync events.
func (h *Handler) HandleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	ctx := r.Context()
	var events []history.SyncEvent
	if h.historyStore != nil {
		var err error
		events, err = h.historyStore.Recent(ctx, 30)
		if err != nil {
			h.logger.Error("failed to query recent sync events", slog.Any("error", err))
			http.Error(w, "Failed to load sync events", http.StatusInternalServerError)
			return
		}
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.tmpl.ExecuteTemplate(w, "layout", IndexData{Events: events}); err != nil {
		h.logger.Error("failed to render index template", slog.Any("error", err))
	}
}
