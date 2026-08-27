package router

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/devops-igor/amnezia-web-ui-go/internal/config"
	"github.com/devops-igor/amnezia-web-ui-go/internal/database"
)

// HealthResponse defines the payload returned by /api/health.
type HealthResponse struct {
	Status  string `json:"status"`
	Version string `json:"version"`
}

// NewRouter sets up the Chi HTTP router, standard middleware stack, and baseline endpoints.
func NewRouter(cfg *config.Config, db *database.DB) *chi.Mux {
	r := chi.NewRouter()

	// Base middleware stack
	r.Use(middleware.RequestID)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(60 * time.Second))

	// Health check endpoint
	r.Get("/api/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(HealthResponse{
			Status:  "ok",
			Version: cfg.AppVersion,
		})
	})

	r.Get("/api/version", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"version": cfg.AppVersion,
		})
	})

	// Subrouter groups placeholders
	r.Route("/api/auth", func(r chi.Router) {})
	r.Route("/api/servers", func(r chi.Router) {})
	r.Route("/api/users", func(r chi.Router) {})
	r.Route("/api/settings", func(r chi.Router) {})
	r.Route("/api/vpn", func(r chi.Router) {})

	return r
}

// Server wraps the standard http.Server with graceful shutdown capability.
type Server struct {
	httpServer *http.Server
}

// NewServer constructs an HTTP server configured with the router and listening address.
func NewServer(cfg *config.Config, handler http.Handler) *Server {
	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	return &Server{
		httpServer: &http.Server{
			Addr:              addr,
			Handler:           handler,
			ReadHeaderTimeout: 10 * time.Second,
			ReadTimeout:       30 * time.Second,
			WriteTimeout:      60 * time.Second,
			IdleTimeout:       120 * time.Second,
		},
	}
}

// Start runs the HTTP server listener.
func (s *Server) Start() error {
	if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// Shutdown initiates graceful shutdown of the HTTP server.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}
