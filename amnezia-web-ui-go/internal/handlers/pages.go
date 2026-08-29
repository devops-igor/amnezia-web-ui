package handlers

import (
	"net/http"

	"github.com/devops-igor/amnezia-web-ui-go/internal/models"
	"github.com/go-chi/chi/v5"
)

// IndexPageHandler renders the primary admin dashboard or redirects normal users.
func (h *Handlers) IndexPageHandler(w http.ResponseWriter, r *http.Request) {
	sess := h.GetSession(r)
	if sess != nil && sess.Role == models.RoleUser {
		http.Redirect(w, r, "/my", http.StatusFound)
		return
	}

	ctx := r.Context()
	servers, _ := h.db.GetAllServers(ctx)

	_ = RenderTemplate(w, r, h.db, "index.html", map[string]any{
		"servers": servers,
	})
}

// UsersPageHandler renders the user management console.
func (h *Handlers) UsersPageHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	users, _ := h.db.GetAllUsers(ctx)
	servers, _ := h.db.GetAllServers(ctx)

	_ = RenderTemplate(w, r, h.db, "users.html", map[string]any{
		"users":   users,
		"servers": servers,
	})
}

// ServerPageHandler renders details and protocol status for a specific server host.
func (h *Handlers) ServerPageHandler(w http.ResponseWriter, r *http.Request) {
	serverIDStr := chi.URLParam(r, "server_id")
	serverID, err := parseServerID(r)
	if err != nil {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}

	ctx := r.Context()
	server, err := h.db.GetServer(ctx, serverID)
	if err != nil || server == nil {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}

	users, _ := h.db.GetAllUsers(ctx)

	_ = RenderTemplate(w, r, h.db, "server.html", map[string]any{
		"server_id": serverIDStr,
		"server":    server,
		"users":     users,
	})
}

// SettingsPageHandler renders system-wide settings and RemnaWave sync panels.
func (h *Handlers) SettingsPageHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	servers, _ := h.db.GetAllServers(ctx)
	settings, _ := h.db.GetAllSettings(ctx)

	_ = RenderTemplate(w, r, h.db, "settings.html", map[string]any{
		"servers":  servers,
		"settings": settings,
	})
}

// MyConnectionsPageHandler renders the user client connection dashboard.
func (h *Handlers) MyConnectionsPageHandler(w http.ResponseWriter, r *http.Request) {
	sess := h.GetSession(r)
	if sess == nil {
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}

	ctx := r.Context()
	conns, _ := h.db.GetConnectionsByUserID(ctx, sess.UserID)
	servers, _ := h.db.GetAllServers(ctx)

	_ = RenderTemplate(w, r, h.db, "my_connections.html", map[string]any{
		"connections": conns,
		"servers":     servers,
	})
}

// SetupPageHandler renders the initial admin account setup wizard.
func (h *Handlers) SetupPageHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	count, _ := h.db.CountUsers(ctx)
	if count > 0 {
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}

	_ = RenderTemplate(w, r, h.db, "setup.html", nil)
}

// ChangePasswordPageHandler renders the password change interface.
func (h *Handlers) ChangePasswordPageHandler(w http.ResponseWriter, r *http.Request) {
	_ = RenderTemplate(w, r, h.db, "change_password.html", nil)
}

// LeaderboardPageHandler renders the traffic leaderboard interface.
func (h *Handlers) LeaderboardPageHandler(w http.ResponseWriter, r *http.Request) {
	_ = RenderTemplate(w, r, h.db, "leaderboard.html", nil)
}
