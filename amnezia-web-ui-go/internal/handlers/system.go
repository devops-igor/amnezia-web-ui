package handlers

import (
	"net/http"

	"github.com/devops-igor/amnezia-web-ui-go/internal/config"
)

// HealthResponse defines the standard payload returned by /api/health.
type HealthResponse struct {
	Status  string `json:"status"`
	Version string `json:"version"`
}

// HealthHandler returns application health status and version.
func (h *Handlers) HealthHandler(w http.ResponseWriter, r *http.Request) {
	version := config.AppVersion
	if h.cfg != nil && h.cfg.AppVersion != "" {
		version = h.cfg.AppVersion
	}

	h.JSON(w, http.StatusOK, HealthResponse{
		Status:  "ok",
		Version: version,
	})
}

// VersionHandler returns current application semantic version.
func (h *Handlers) VersionHandler(w http.ResponseWriter, r *http.Request) {
	version := config.AppVersion
	if h.cfg != nil && h.cfg.AppVersion != "" {
		version = h.cfg.AppVersion
	}

	h.JSON(w, http.StatusOK, map[string]string{
		"version": version,
	})
}
