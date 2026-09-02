package router

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"

	"github.com/devops-igor/amnezia-web-ui-go/internal/config"
	"github.com/devops-igor/amnezia-web-ui-go/internal/database"
	"github.com/devops-igor/amnezia-web-ui-go/internal/handlers"
	"github.com/devops-igor/amnezia-web-ui-go/internal/manager"
	"github.com/devops-igor/amnezia-web-ui-go/internal/manager/awg"
	"github.com/devops-igor/amnezia-web-ui-go/internal/manager/dns"
	"github.com/devops-igor/amnezia-web-ui-go/internal/manager/mtproxyl"
	"github.com/devops-igor/amnezia-web-ui-go/internal/manager/ssh"
	"github.com/devops-igor/amnezia-web-ui-go/internal/middleware"
	"github.com/devops-igor/amnezia-web-ui-go/internal/models"
	"github.com/devops-igor/amnezia-web-ui-go/internal/vpn"
	"github.com/devops-igor/amnezia-web-ui-go/web"
)

// HealthResponse defines the payload returned by /api/health.
type HealthResponse = handlers.HealthResponse

// Options holds dependencies, handlers, and limiters for the HTTP router.
type Options struct {
	Config       *config.Config
	DB           *database.DB
	Handlers     *handlers.Handlers
	LoginLimiter *middleware.RateLimiter
	APILimiter   *middleware.RateLimiter
}

// NewRouter sets up the Chi HTTP router, standard middleware stack, and all route groups.
func NewRouter(cfg *config.Config, db *database.DB) *chi.Mux {
	sshPool := ssh.NewSSHClientPool(ssh.PoolConfig{
		IdleTimeout:     5 * time.Minute,
		KeepAlivePeriod: 30 * time.Second,
	}, db)

	awgMgr := awg.NewAWGManager(sshPool)
	mtproxylMgr := mtproxyl.NewMTProxyLManager(sshPool)
	dnsMgr := dns.NewDNSManager(sshPool)
	vpnSvc, _ := vpn.NewVPNService(db, nil)

	reg := manager.NewRegistry()
	reg.Register(awgMgr)
	reg.Register(mtproxylMgr)
	reg.Register(dnsMgr)

	h := handlers.NewHandlers(handlers.Dependencies{
		Config:          cfg,
		DB:              db,
		Registry:        reg,
		SSHPool:         sshPool,
		AWGManager:      awgMgr,
		MTProxyLManager: mtproxylMgr,
		DNSManager:      dnsMgr,
		VPNService:      vpnSvc,
	})

	return NewRouterWithOptions(Options{
		Config:   cfg,
		DB:       db,
		Handlers: h,
	})
}

// NewRouterWithOptions builds the complete router given explicit Options and dependencies.
func NewRouterWithOptions(opts Options) *chi.Mux {
	r := chi.NewRouter()
	cfg := opts.Config
	db := opts.DB
	h := opts.Handlers

	if h == nil {
		h = handlers.NewHandlers(handlers.Dependencies{
			Config: cfg,
			DB:     db,
		})
	}

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
		middleware.SetUserLookup(func(ctx context.Context, userID string) (*models.User, error) {
			return db.GetUser(ctx, userID)
		})
		r.Use(middleware.SetupRedirect(db))
	}
	r.Use(middleware.PasswordChangeRequired())
	r.Use(middleware.CSRF(false))

	// Limiters
	isE2E := strings.EqualFold(os.Getenv("E2E_TESTING"), "true") || os.Getenv("E2E_TESTING") == "1"
	if isE2E {
		slog.Warn("E2E_TESTING is enabled: rate limiting is disabled — DO NOT USE IN PRODUCTION")
	}
	loginLimiter := opts.LoginLimiter
	if loginLimiter == nil {
		if isE2E {
			loginLimiter = middleware.NewRateLimiterPerMinute(100000, 100000)
		} else {
			loginLimiter = middleware.NewRateLimiterPerMinute(5, 5)
		}
	}
	apiLimiter := opts.APILimiter
	if apiLimiter == nil {
		if isE2E {
			apiLimiter = middleware.NewRateLimiterPerMinute(100000, 100000)
		} else {
			apiLimiter = middleware.NewRateLimiterPerMinute(60, 60)
		}
	}

	// 2. Static Assets Serving
	serveStatic(r)

	// 3. Health and Version Endpoints (Public)
	r.Get("/api/health", h.HealthHandler)
	r.Get("/api/version", h.VersionHandler)

	// 4. Public Page Routes
	r.Get("/login", h.LoginHandler)
	r.Get("/setup", h.SetupPageHandler)
	r.Get("/leaderboard", h.LeaderboardPageHandler)
	r.Get("/share/{token}", h.SharePageHandler)
	r.Get("/logout", h.LogoutHandler)
	r.Get("/set_lang/{lang}", h.SetLangHandler)

	// 5. Auth API Group
	r.Route("/api/auth", func(r chi.Router) {
		r.Get("/captcha", h.CaptchaHandler)
		r.With(middleware.RateLimit(loginLimiter)).Post("/login", h.APILoginHandler)
		r.With(middleware.RateLimit(loginLimiter)).Post("/setup", h.APISetupHandler)
		r.With(middleware.RequireAuth).Post("/change-password", h.APIChangePasswordHandler)
	})

	// 6. User-facing Session Protected Pages & APIs
	r.Group(func(r chi.Router) {
		r.Use(middleware.RequireAuth)

		r.Get("/change-password", h.ChangePasswordPageHandler)
		r.Get("/my", h.MyConnectionsPageHandler)
		r.Get("/api/my/connections", h.UserGetMyConnectionsHandler)

		r.Route("/api/connections", func(r chi.Router) {
			r.Use(middleware.RateLimit(apiLimiter))

			r.Get("/", h.UserGetMyConnectionsHandler)
			r.Post("/add", h.UserAddConnectionHandler)
			r.Post("/{connection_id}/config", h.UserGetConnectionConfigHandler)
			r.Post("/{connection_id}/kit", h.UserGetConnectionKitHandler)
			r.Post("/{connection_id}/rename", h.UserRenameConnectionHandler)
			r.Post("/{connection_id}/delete", h.UserDeleteConnectionHandler)
		})

		r.Get("/api/vpn/my-connection", h.VPNMyConnectionHandler)
		r.Get("/api/vpn/my-config", h.VPNMyConfigHandler)

		// Server management API: GET is accessible to any authenticated user (with role-based sanitization),
		// while administrative mutating endpoints require RequireAdminOrSupport.
		r.Route("/api/servers", func(r chi.Router) {
			r.Get("/", h.ListServersHandler)

			r.Group(func(r chi.Router) {
				r.Use(middleware.RequireAdminOrSupport)
				r.Post("/add", h.AddServerHandler)
				r.Post("/confirm-fingerprint", h.ConfirmFingerprintHandler)
				r.Post("/{server_id}/delete", h.DeleteServerHandler)
				r.Post("/{server_id}/reboot", h.RebootServerHandler)
				r.Post("/{server_id}/clear", h.ClearServerHandler)
				r.Post("/{server_id}/stats", h.ServerStatsHandler)
				r.Post("/{server_id}/check", h.ServerCheckHandler)
				r.Post("/{server_id}/install", h.InstallProtocolHandler)
				r.Post("/{server_id}/uninstall", h.UninstallProtocolHandler)
				r.Post("/{server_id}/container/toggle", h.ToggleContainerHandler)
				r.Post("/{server_id}/server_config", h.GetServerConfigHandler)
				r.Post("/{server_id}/server_config/save", h.SaveServerConfigHandler)
				r.Get("/{server_id}/connections", h.GetServerConnectionsHandler)
				r.Post("/{server_id}/connections/add", h.AddServerConnectionHandler)
				r.Post("/{server_id}/connections/{client_id}/rotate-mimicry", h.RotateMimicryHandler)
				r.Get("/{server_id}/reachability", h.GetServerReachabilityHandler)
				r.Post("/{server_id}/connections/auto-trial", h.AutoTrialHandler)
				r.Post("/{server_id}/connections/kit", h.GetServerConnectionKitHandler)
				r.Post("/{server_id}/connections/remove", h.RemoveServerConnectionHandler)
				r.Post("/{server_id}/connections/edit", h.EditServerConnectionHandler)
				r.Post("/{server_id}/connections/config", h.GetServerConnectionConfigHandler)
				r.Post("/{server_id}/connections/toggle", h.ToggleServerConnectionHandler)
				r.Get("/{server_id}/{protocol}/clients", h.GetProtocolClientsHandler)
				r.Patch("/{server_id}/connections/speed-limit", h.SetClientSpeedLimitHandler)
				r.Get("/{server_id}/awg/speed-limit-config", h.GetAWGSpeedLimitConfigHandler)
				r.Patch("/{server_id}/awg/speed-limit-config", h.SetAWGSpeedLimitConfigHandler)
				r.Post("/{server_id}/awg/apply-default-speed-limits", h.ApplyDefaultSpeedLimitsHandler)
			})
		})
	})

	// 7. Admin Protected Pages & APIs
	r.Group(func(r chi.Router) {
		r.Use(middleware.RequireAdminOrSupport)

		r.Get("/", h.IndexPageHandler)
		r.Get("/server/{server_id}", h.ServerPageHandler)
		r.Get("/users", h.UsersPageHandler)
		r.Get("/settings", h.SettingsPageHandler)

		// Root server management aliases matching legacy routes
		r.Post("/add", h.AddServerHandler)
		r.Post("/confirm-fingerprint", h.ConfirmFingerprintHandler)
		r.Post("/{server_id}/delete", h.DeleteServerHandler)
		r.Post("/{server_id}/reboot", h.RebootServerHandler)
		r.Post("/{server_id}/clear", h.ClearServerHandler)
		r.Post("/{server_id}/stats", h.ServerStatsHandler)
		r.Post("/{server_id}/check", h.ServerCheckHandler)
		r.Post("/{server_id}/install", h.InstallProtocolHandler)
		r.Post("/{server_id}/uninstall", h.UninstallProtocolHandler)
		r.Post("/{server_id}/container/toggle", h.ToggleContainerHandler)
		r.Post("/{server_id}/server_config", h.GetServerConfigHandler)
		r.Post("/{server_id}/server_config/save", h.SaveServerConfigHandler)
		r.Get("/{server_id}/connections", h.GetServerConnectionsHandler)
		r.Post("/{server_id}/connections/add", h.AddServerConnectionHandler)
		r.Post("/{server_id}/connections/{client_id}/rotate-mimicry", h.RotateMimicryHandler)
		r.Get("/{server_id}/reachability", h.GetServerReachabilityHandler)
		r.Post("/{server_id}/connections/auto-trial", h.AutoTrialHandler)
		r.Post("/{server_id}/connections/kit", h.GetServerConnectionKitHandler)
		r.Post("/{server_id}/connections/remove", h.RemoveServerConnectionHandler)
		r.Post("/{server_id}/connections/edit", h.EditServerConnectionHandler)
		r.Post("/{server_id}/connections/config", h.GetServerConnectionConfigHandler)
		r.Post("/{server_id}/connections/toggle", h.ToggleServerConnectionHandler)
		r.Get("/{server_id}/{protocol}/clients", h.GetProtocolClientsHandler)
		r.Patch("/{server_id}/connections/speed-limit", h.SetClientSpeedLimitHandler)
		r.Get("/{server_id}/awg/speed-limit-config", h.GetAWGSpeedLimitConfigHandler)
		r.Patch("/{server_id}/awg/speed-limit-config", h.SetAWGSpeedLimitConfigHandler)
		r.Post("/{server_id}/awg/apply-default-speed-limits", h.ApplyDefaultSpeedLimitsHandler)

		// User management API
		r.Route("/api/users", func(r chi.Router) {
			r.Get("/", h.ListUsersHandler)
			r.Post("/add", h.AddUserHandler)
			r.Post("/{user_id}/update", h.UpdateUserHandler)
			r.Post("/{user_id}/delete", h.DeleteUserHandler)
			r.Post("/{user_id}/toggle", h.ToggleUserHandler)
			r.Post("/{user_id}/connections/add", h.AddUserConnectionHandler)
			r.Get("/{user_id}/connections", h.GetUserConnectionsHandler)
			r.Post("/{user_id}/share/setup", h.SetupUserShareHandler)
		})

		// Settings API
		r.Route("/api/settings", func(r chi.Router) {
			r.Get("/", h.GetSettingsHandler)
			r.Post("/save", h.SaveSettingsHandler)
			r.Post("/sync_now", h.SyncNowHandler)
			r.Post("/sync_delete", h.SyncDeleteHandler)
			r.Get("/backup/download", h.DownloadBackupHandler)
			r.Post("/backup/restore", h.RestoreBackupHandler)
		})

		// VPN Subsystem API (Admin endpoints)
		r.Route("/api/vpn", func(r chi.Router) {
			r.Get("/status", h.VPNStatusHandler)
			r.Get("/backends", h.VPNBackendsHandler)
			r.Post("/backends/{server_id}/enable", h.VPNEnableBackendHandler)
			r.Post("/backends/{server_id}/disable", h.VPNDisableBackendHandler)
			r.Get("/tunnels", h.VPNTunnelsHandler)
			r.Get("/config", h.VPNGetConfigHandler)
			r.Post("/config", h.VPNUpdateConfigHandler)
			r.Post("/disconnect", h.VPNDisconnectHandler)
		})
	})

	// 8. Public / Share API Group
	r.Get("/api/leaderboard", h.LeaderboardHandler)
	r.Post("/api/share/{token}/auth", h.ShareAuthHandler)
	r.Get("/api/share/{token}/connections", h.GetShareConnectionsHandler)
	r.Post("/api/share/{token}/config/{connection_id}", h.GetShareConnectionConfigHandler)

	return r
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
