package handlers

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/devops-igor/amnezia-web-ui-go/internal/models"
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

// UsersPageHandler renders the multi-user management interface.
func (h *Handlers) UsersPageHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	users, _ := h.db.GetAllUsers(ctx)
	servers, _ := h.db.GetAllServers(ctx)

	_ = RenderTemplate(w, r, h.db, "users.html", map[string]any{
		"users":   users,
		"servers": servers,
	})
}

// ServerPageHandler renders a single server management interface.
func (h *Handlers) ServerPageHandler(w http.ResponseWriter, r *http.Request) {
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
		"server":    server,
		"server_id": serverID,
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

// SanitizedServerForUser represents a safe server view for regular panel users.
type SanitizedServerForUser struct {
	ID        int64                      `json:"id"`
	Name      string                     `json:"name"`
	Protocols map[string]map[string]bool `json:"protocols"`
	Status    string                     `json:"status,omitempty"`
	Reachable bool                       `json:"reachable"`
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
	rawServers, _ := h.db.GetAllServers(ctx)

	sanitizedServers := make([]SanitizedServerForUser, 0, len(rawServers))
	serversMap := make(map[int64]SanitizedServerForUser)
	for _, srv := range rawServers {
		name := srv.Name
		if strings.TrimSpace(name) == "" {
			name = fmt.Sprintf("Server #%d", srv.ID)
		}
		protoMap := make(map[string]map[string]bool)
		for proto, pVal := range srv.Protocols {
			installed := false
			if m, ok := pVal.(map[string]any); ok {
				if inst, ok := m["installed"].(bool); ok {
					installed = inst
				}
			} else if b, ok := pVal.(bool); ok {
				installed = b
			}
			protoMap[proto] = map[string]bool{"installed": installed}
		}
		status := string(srv.Status)
		if status == "" {
			status = "online"
		}
		reachable := srv.Status == models.ReachabilityOnline || srv.Status == ""
		sClean := SanitizedServerForUser{
			ID:        srv.ID,
			Name:      name,
			Protocols: protoMap,
			Status:    status,
			Reachable: reachable,
		}
		sanitizedServers = append(sanitizedServers, sClean)
		serversMap[srv.ID] = sClean
	}

	for i := range conns {
		if sClean, ok := serversMap[conns[i].ServerID]; ok {
			conns[i].ServerName = sClean.Name
		} else if conns[i].ServerID > 0 {
			conns[i].ServerName = fmt.Sprintf("Server #%d", conns[i].ServerID)
		}
	}

	_ = RenderTemplate(w, r, h.db, "my_connections.html", map[string]any{
		"connections": conns,
		"servers":     sanitizedServers,
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
	forced := r.URL.Query().Get("forced") == "1"
	_ = RenderTemplate(w, r, h.db, "change_password.html", map[string]any{
		"forced": forced,
	})
}

// LeaderboardPageHandler renders the traffic leaderboard interface.
func (h *Handlers) LeaderboardPageHandler(w http.ResponseWriter, r *http.Request) {
	period := r.URL.Query().Get("period")
	if period != "monthly" && period != "last-month" {
		period = "all-time"
	}

	var monthlyLabel *string
	now := time.Now()
	if period == "monthly" {
		label := now.Format("January 2006")
		monthlyLabel = &label
	} else if period == "last-month" {
		lastMonth := now.AddDate(0, -1, 0)
		label := lastMonth.Format("January 2006")
		monthlyLabel = &label
	}

	ctx := r.Context()
	entries, _ := h.db.GetLeaderboard(ctx, period)

	sess := h.GetSession(r)
	var currentUserRank *int
	for _, e := range entries {
		if sess != nil && sess.Username == e.Username {
			rankVal := e.Rank
			currentUserRank = &rankVal
			break
		}
	}

	_ = RenderTemplate(w, r, h.db, "leaderboard.html", map[string]any{
		"period":            period,
		"entries":           entries,
		"current_user_rank": currentUserRank,
		"monthly_label":     monthlyLabel,
	})
}
