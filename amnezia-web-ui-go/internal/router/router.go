package router

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"

	"github.com/devops-igor/amnezia-web-ui-go/internal/config"
	"github.com/devops-igor/amnezia-web-ui-go/internal/database"
	"github.com/devops-igor/amnezia-web-ui-go/internal/middleware"
	"github.com/devops-igor/amnezia-web-ui-go/web"
)

// HealthResponse defines the payload returned by /api/health.
type HealthResponse struct {
	Status  string `json:"status"`
	Version string `json:"version"`
}

// Options holds dependencies and limiters for the HTTP router.
type Options struct {
	Config       *config.Config
	DB           *database.DB
	LoginLimiter *middleware.RateLimiter
	APILimiter   *middleware.RateLimiter
}

// NewRouter sets up the Chi HTTP router, standard middleware stack, and all route groups.
func NewRouter(cfg *config.Config, db *database.DB) *chi.Mux {
	r := chi.NewRouter()

	// 1. Global Base Middleware Stack
	r.Use(chimiddleware.RequestID)
	if cfg != nil {
		r.Use(middleware.RealIP(cfg.TrustedCIDRs, cfg.TrustedIPs))
	}
	r.Use(chimiddleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(chimiddleware.Timeout(60 * time.Second))

	if cfg != nil && cfg.SecretKey != "" {
		r.Use(middleware.Session(cfg.SecretKey))
	}
	if db != nil {
		r.Use(middleware.SetupRedirect(db))
	}
	r.Use(middleware.PasswordChangeRequired())
	r.Use(middleware.CSRF(false))

	// Limiters
	loginLimiter := middleware.NewRateLimiterPerMinute(5, 5)
	apiLimiter := middleware.NewRateLimiterPerMinute(60, 60)

	// 2. Static Assets Serving
	serveStatic(r)

	// 3. Health and Version Endpoints (Public)
	r.Get("/api/health", func(w http.ResponseWriter, r *http.Request) {
		version := config.AppVersion
		if cfg != nil {
			version = cfg.AppVersion
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(HealthResponse{
			Status:  "ok",
			Version: version,
		})
	})

	r.Get("/api/version", func(w http.ResponseWriter, r *http.Request) {
		version := config.AppVersion
		if cfg != nil {
			version = cfg.AppVersion
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"version": version,
		})
	})

	// 4. Public Page Routes
	r.Get("/login", func(w http.ResponseWriter, r *http.Request) {
		sess := middleware.GetSession(r.Context())
		if sess != nil && sess.IsAuthenticated() {
			http.Redirect(w, r, "/", http.StatusFound)
			return
		}
		_ = RenderTemplate(w, r, db, "login.html", nil)
	})

	r.Get("/setup", func(w http.ResponseWriter, r *http.Request) {
		_ = RenderTemplate(w, r, db, "setup.html", nil)
	})

	r.Get("/leaderboard", func(w http.ResponseWriter, r *http.Request) {
		_ = RenderTemplate(w, r, db, "leaderboard.html", nil)
	})

	r.Get("/share/{token}", func(w http.ResponseWriter, r *http.Request) {
		token := chi.URLParam(r, "token")
		_ = RenderTemplate(w, r, db, "user_share.html", map[string]any{
			"token": token,
		})
	})

	r.Get("/logout", func(w http.ResponseWriter, r *http.Request) {
		middleware.ClearSessionCookie(w)
		http.Redirect(w, r, "/login", http.StatusFound)
	})

	r.Get("/set_lang/{lang}", func(w http.ResponseWriter, r *http.Request) {
		lang := chi.URLParam(r, "lang")
		ref := CleanReferer(r.Header.Get("Referer"))
		// #nosec G124 -- Language preference cookie
		http.SetCookie(w, &http.Cookie{
			Name:     "lang",
			Value:    lang,
			Path:     "/",
			MaxAge:   31536000,
			SameSite: http.SameSiteLaxMode,
		})
		// #nosec G124 -- Language preference cookie
		http.SetCookie(w, &http.Cookie{
			Name:     "panel_lang",
			Value:    lang,
			Path:     "/",
			MaxAge:   31536000,
			SameSite: http.SameSiteLaxMode,
		})
		// #nosec G710,G116 -- Open redirect prevented by CleanReferer
		http.Redirect(w, r, ref, http.StatusFound)
	})

	// 5. Auth API Group
	r.Route("/api/auth", func(r chi.Router) {
		r.Get("/captcha", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"captcha_id": "captcha-placeholder",
				"image":      "data:image/png;base64,",
			})
		})

		r.With(middleware.RateLimit(loginLimiter)).Post("/login", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{
				"status":   "ok",
				"redirect": "/",
			})
		})

		r.With(middleware.RateLimit(loginLimiter)).Post("/setup", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{
				"status":   "ok",
				"redirect": "/",
			})
		})

		r.With(middleware.RequireAuth).Post("/change-password", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{
				"status":  "ok",
				"message": "Password updated",
			})
		})
	})

	// 6. User-facing Session Protected Pages & APIs
	r.Group(func(r chi.Router) {
		r.Use(middleware.RequireAuth)

		r.Get("/change-password", func(w http.ResponseWriter, r *http.Request) {
			_ = RenderTemplate(w, r, db, "change_password.html", nil)
		})

		r.Get("/my", func(w http.ResponseWriter, r *http.Request) {
			_ = RenderTemplate(w, r, db, "my_connections.html", nil)
		})

		r.Route("/api/connections", func(r chi.Router) {
			r.Use(middleware.RateLimit(apiLimiter))

			r.Post("/add", jsonOKHandler)
			r.Post("/{connection_id}/config", jsonOKHandler)
			r.Post("/{connection_id}/kit", jsonOKHandler)
			r.Post("/{connection_id}/rename", jsonOKHandler)
			r.Post("/{connection_id}/delete", jsonOKHandler)
		})

		r.Get("/api/vpn/my-connection", jsonOKHandler)
		r.Get("/api/vpn/my-config", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{
				"status":   "ok",
				"config":   "[Interface]\n",
				"filename": "portal-awg.conf",
			})
		})
	})

	// 7. Admin Protected Pages & APIs
	r.Group(func(r chi.Router) {
		r.Use(middleware.RequireAdminOrSupport)

		r.Get("/", func(w http.ResponseWriter, r *http.Request) {
			_ = RenderTemplate(w, r, db, "index.html", nil)
		})

		r.Get("/server/{server_id}", func(w http.ResponseWriter, r *http.Request) {
			serverID := chi.URLParam(r, "server_id")
			_ = RenderTemplate(w, r, db, "server.html", map[string]any{
				"server_id": serverID,
			})
		})

		r.Get("/users", func(w http.ResponseWriter, r *http.Request) {
			_ = RenderTemplate(w, r, db, "users.html", nil)
		})

		r.Get("/settings", func(w http.ResponseWriter, r *http.Request) {
			_ = RenderTemplate(w, r, db, "settings.html", nil)
		})

		// Server management API
		r.Route("/api/servers", func(r chi.Router) {
			r.Post("/add", jsonOKHandler)
			r.Post("/confirm-fingerprint", jsonOKHandler)
			r.Post("/{server_id}/delete", jsonOKHandler)
			r.Post("/{server_id}/reboot", jsonOKHandler)
			r.Post("/{server_id}/clear", jsonOKHandler)
			r.Post("/{server_id}/stats", jsonOKHandler)
			r.Post("/{server_id}/check", jsonOKHandler)
			r.Post("/{server_id}/install", jsonOKHandler)
			r.Post("/{server_id}/uninstall", jsonOKHandler)
			r.Post("/{server_id}/container/toggle", jsonOKHandler)
			r.Post("/{server_id}/server_config", jsonOKHandler)
			r.Post("/{server_id}/server_config/save", jsonOKHandler)
			r.Get("/{server_id}/connections", jsonOKHandler)
			r.Post("/{server_id}/connections/add", jsonOKHandler)
			r.Post("/{server_id}/connections/{client_id}/rotate-mimicry", jsonOKHandler)
			r.Get("/{server_id}/reachability", jsonOKHandler)
			r.Post("/{server_id}/connections/auto-trial", jsonOKHandler)
			r.Post("/{server_id}/connections/kit", jsonOKHandler)
			r.Post("/{server_id}/connections/remove", jsonOKHandler)
			r.Post("/{server_id}/connections/edit", jsonOKHandler)
			r.Post("/{server_id}/connections/config", jsonOKHandler)
			r.Post("/{server_id}/connections/toggle", jsonOKHandler)
			r.Get("/{server_id}/{protocol}/clients", jsonOKHandler)
			r.Patch("/{server_id}/connections/speed-limit", jsonOKHandler)
			r.Get("/{server_id}/awg/speed-limit-config", jsonOKHandler)
			r.Patch("/{server_id}/awg/speed-limit-config", jsonOKHandler)
			r.Post("/{server_id}/awg/apply-default-speed-limits", jsonOKHandler)
		})

		// Root server management aliases matching legacy routes
		r.Post("/add", jsonOKHandler)
		r.Post("/confirm-fingerprint", jsonOKHandler)
		r.Post("/{server_id}/delete", jsonOKHandler)
		r.Post("/{server_id}/reboot", jsonOKHandler)
		r.Post("/{server_id}/clear", jsonOKHandler)
		r.Post("/{server_id}/stats", jsonOKHandler)
		r.Post("/{server_id}/check", jsonOKHandler)
		r.Post("/{server_id}/install", jsonOKHandler)
		r.Post("/{server_id}/uninstall", jsonOKHandler)
		r.Post("/{server_id}/container/toggle", jsonOKHandler)
		r.Post("/{server_id}/server_config", jsonOKHandler)
		r.Post("/{server_id}/server_config/save", jsonOKHandler)
		r.Get("/{server_id}/connections", jsonOKHandler)
		r.Post("/{server_id}/connections/add", jsonOKHandler)
		r.Post("/{server_id}/connections/{client_id}/rotate-mimicry", jsonOKHandler)
		r.Get("/{server_id}/reachability", jsonOKHandler)
		r.Post("/{server_id}/connections/auto-trial", jsonOKHandler)
		r.Post("/{server_id}/connections/kit", jsonOKHandler)
		r.Post("/{server_id}/connections/remove", jsonOKHandler)
		r.Post("/{server_id}/connections/edit", jsonOKHandler)
		r.Post("/{server_id}/connections/config", jsonOKHandler)
		r.Post("/{server_id}/connections/toggle", jsonOKHandler)
		r.Get("/{server_id}/{protocol}/clients", jsonOKHandler)
		r.Patch("/{server_id}/connections/speed-limit", jsonOKHandler)
		r.Get("/{server_id}/awg/speed-limit-config", jsonOKHandler)
		r.Patch("/{server_id}/awg/speed-limit-config", jsonOKHandler)
		r.Post("/{server_id}/awg/apply-default-speed-limits", jsonOKHandler)

		// User management API
		r.Route("/api/users", func(r chi.Router) {
			r.Post("/add", jsonOKHandler)
			r.Post("/{user_id}/update", jsonOKHandler)
			r.Post("/{user_id}/delete", jsonOKHandler)
			r.Post("/{user_id}/toggle", jsonOKHandler)
			r.Post("/{user_id}/connections/add", jsonOKHandler)
			r.Get("/{user_id}/connections", jsonOKHandler)
			r.Post("/{user_id}/share/setup", jsonOKHandler)
		})

		// Settings API
		r.Route("/api/settings", func(r chi.Router) {
			r.Get("/", jsonOKHandler)
			r.Post("/save", jsonOKHandler)
			r.Post("/sync_now", jsonOKHandler)
			r.Post("/sync_delete", jsonOKHandler)
			r.Get("/backup/download", jsonOKHandler)
			r.Post("/backup/restore", jsonOKHandler)
		})

		// VPN Subsystem API (Admin endpoints)
		r.Route("/api/vpn", func(r chi.Router) {
			r.Get("/status", jsonOKHandler)
			r.Get("/backends", jsonOKHandler)
			r.Post("/backends/{server_id}/enable", jsonOKHandler)
			r.Post("/backends/{server_id}/disable", jsonOKHandler)
			r.Get("/tunnels", jsonOKHandler)
			r.Get("/config", jsonOKHandler)
			r.Post("/config", jsonOKHandler)
			r.Post("/disconnect", jsonOKHandler)
		})
	})

	// 8. Public / Share API Group
	r.Get("/api/leaderboard", jsonOKHandler)
	r.Post("/api/share/{token}/auth", jsonOKHandler)
	r.Get("/api/share/{token}/connections", jsonOKHandler)
	r.Post("/api/share/{token}/config/{connection_id}", jsonOKHandler)

	return r
}

func jsonOKHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func serveStatic(r *chi.Mux) {
	staticSubFS, err := web.GetStaticSubFS()
	if err == nil {
		fileServer := http.FileServer(http.FS(staticSubFS))
		r.Handle("/static/*", http.StripPrefix("/static", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "public, max-age=86400")
			fileServer.ServeHTTP(w, r)
		})))
	}
}
