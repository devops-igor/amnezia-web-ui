package handlers

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/devops-igor/amnezia-web-ui-go/internal/database"
	"github.com/devops-igor/amnezia-web-ui-go/internal/middleware"
	"github.com/devops-igor/amnezia-web-ui-go/internal/models"
	"github.com/devops-igor/amnezia-web-ui-go/internal/security"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// LoginHandler renders the login HTML page or redirects authenticated users.
func (h *Handlers) LoginHandler(w http.ResponseWriter, r *http.Request) {
	sess := h.GetSession(r)
	if sess != nil && sess.IsAuthenticated() {
		if sess.Role == models.RoleUser {
			http.Redirect(w, r, "/my", http.StatusFound)
			return
		}
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	_ = RenderTemplate(w, r, h.db, "login.html", nil)
}

// LogoutHandler clears active session cookies and redirects to /login.
func (h *Handlers) LogoutHandler(w http.ResponseWriter, r *http.Request) {
	h.audit(r, "auth.logout", nil)
	middleware.ClearSessionCookie(w)
	http.Redirect(w, r, "/login", http.StatusFound)
}

// SetLangHandler sets preferred UI language cookie and redirects back safely.
func (h *Handlers) SetLangHandler(w http.ResponseWriter, r *http.Request) {
	lang := strings.ToLower(strings.TrimSpace(chi.URLParam(r, "lang")))
	switch lang {
	case "en", "ru":
		// valid
	default:
		lang = "en"
	}
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
}

// CaptchaHandler generates a new visual CAPTCHA challenge.
func (h *Handlers) CaptchaHandler(w http.ResponseWriter, r *http.Request) {
	if h.cfg == nil || h.cfg.SecretKey == "" {
		h.JSONError(w, http.StatusInternalServerError, "internal_error", "Session signing key not configured")
		return
	}

	captchaAnswer := generateCaptchaDigits(4)

	// Store answer in session
	sess := h.GetSession(r)
	if sess == nil {
		sess = &models.SessionData{}
	}
	sess.CaptchaAnswer = captchaAnswer
	_ = middleware.SetSessionCookie(w, sess, h.cfg.SecretKey, false, 3600)

	// Generate image
	imgBytes := generateCaptchaImage(captchaAnswer)
	imgB64 := base64.StdEncoding.EncodeToString(imgBytes)

	// If client expects image directly
	accept := r.Header.Get("Accept")
	if strings.Contains(accept, "image/") {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(imgBytes)
		return
	}

	h.JSON(w, http.StatusOK, map[string]any{
		"captcha_id": fmt.Sprintf("captcha-%d", time.Now().UnixNano()),
		"image":      fmt.Sprintf("data:image/png;base64,%s", imgB64),
	})
}

// APILoginHandler handles user authentication requests.
func (h *Handlers) APILoginHandler(w http.ResponseWriter, r *http.Request) {
	if h.cfg == nil || h.cfg.SecretKey == "" {
		h.JSONError(w, http.StatusInternalServerError, "internal_error", "Session signing key not configured")
		return
	}

	var req models.LoginRequest
	if err := h.DecodeJSON(r, &req); err != nil {
		h.JSONError(w, http.StatusBadRequest, "validation_failed", "Invalid request body")
		return
	}

	if err := req.Validate(); err != nil {
		h.JSONError(w, http.StatusBadRequest, "validation_failed", err.Error())
		return
	}

	ctx := r.Context()

	// Check CAPTCHA if enabled
	var captchaCfg models.CaptchaSettings
	if h.db != nil {
		_ = h.db.GetSetting(ctx, "captcha", &captchaCfg)
	}

	if captchaCfg.Enabled {
		sess := h.GetSession(r)
		expected := ""
		if sess != nil {
			expected = sess.CaptchaAnswer
		}

		if expected == "" || req.Captcha == nil || !strings.EqualFold(strings.TrimSpace(*req.Captcha), expected) {
			// Clear captcha answer to prevent replay
			if sess != nil {
				sess.CaptchaAnswer = ""
				_ = middleware.SetSessionCookie(w, sess, h.cfg.SecretKey, false, 3600)
			}
			h.JSONError(w, http.StatusBadRequest, "invalid_captcha", h.Translate(r, "invalid_captcha"))
			return
		}

		// Clear captcha answer after successful verification
		if sess != nil {
			sess.CaptchaAnswer = ""
			_ = middleware.SetSessionCookie(w, sess, h.cfg.SecretKey, false, 3600)
		}
	}

	if h.db == nil {
		h.JSONError(w, http.StatusInternalServerError, "internal_error", "Database not initialized")
		return
	}

	user, err := h.db.GetUserByUsername(ctx, req.Username)
	if err != nil || user == nil {
		h.JSONError(w, http.StatusUnauthorized, "unauthorized", h.Translate(r, "invalid_login"))
		return
	}

	if !security.CheckPasswordHash(req.Password, user.PasswordHash) {
		h.JSONError(w, http.StatusUnauthorized, "unauthorized", h.Translate(r, "invalid_login"))
		return
	}

	if !user.Enabled {
		h.JSONError(w, http.StatusForbidden, "forbidden", h.Translate(r, "account_disabled"))
		return
	}

	// Create session data
	sessionData := &models.SessionData{
		UserID:                 user.ID,
		Username:               user.Username,
		Role:                   user.Role,
		PasswordChangeRequired: user.PasswordChangeRequired,
		ShareAuthenticated:     make(map[string]bool),
	}

	if err := middleware.SetSessionCookie(w, sessionData, h.cfg.SecretKey, false, middleware.DefaultSessionMaxAge); err != nil {
		h.JSONError(w, http.StatusInternalServerError, "internal_error", "Failed to create session")
		return
	}

	redirectURL := "/"
	if user.Role == models.RoleUser {
		redirectURL = "/my"
	}

	h.audit(r, "auth.login", map[string]any{"user_id": user.ID, "username": user.Username, "role": string(user.Role)})

	h.JSON(w, http.StatusOK, map[string]any{
		"status":                   "ok",
		"role":                     string(user.Role),
		"password_change_required": user.PasswordChangeRequired,
		"redirect":                 redirectURL,
	})
}

// APISetupHandler creates the initial administrator user on first run.
func (h *Handlers) APISetupHandler(w http.ResponseWriter, r *http.Request) {
	h.setupMu.Lock()
	defer h.setupMu.Unlock()

	var req models.SetupRequest
	if err := h.DecodeJSON(r, &req); err != nil {
		h.JSONError(w, http.StatusBadRequest, "validation_failed", "Invalid request body")
		return
	}

	if err := req.Validate(); err != nil {
		h.JSONError(w, http.StatusBadRequest, "validation_failed", err.Error())
		return
	}

	ctx := r.Context()
	if h.db == nil {
		h.JSONError(w, http.StatusInternalServerError, "internal_error", "Database not initialized")
		return
	}

	userCount, err := h.db.CountUsers(ctx)
	if err != nil {
		h.JSONError(w, http.StatusInternalServerError, "internal_error", "Failed to check existing users")
		return
	}

	if userCount > 0 {
		h.JSONError(w, http.StatusConflict, "setup_already_done", h.Translate(r, "setup_already_done"))
		return
	}

	hash, err := security.HashPassword(req.Password)
	if err != nil {
		h.JSONError(w, http.StatusInternalServerError, "internal_error", "Failed to hash password")
		return
	}

	adminUser := &models.User{
		ID:                     uuid.NewString(),
		Username:               req.Username,
		PasswordHash:           hash,
		Role:                   models.RoleAdmin,
		Enabled:                true,
		PasswordChangeRequired: false,
		CreatedAt:              time.Now(),
	}

	if _, err := h.db.CreateUser(ctx, adminUser); err != nil {
		if errors.Is(err, database.ErrUserAlreadyExists) || strings.Contains(err.Error(), "user already exists") || strings.Contains(err.Error(), "UNIQUE constraint") {
			h.JSONError(w, http.StatusConflict, "setup_already_done", h.Translate(r, "setup_already_done"))
			return
		}
		h.JSONError(w, http.StatusInternalServerError, "internal_error", "Failed to create administrator account")
		return
	}

	middleware.InvalidateSetupCache()

	h.audit(r, "auth.setup", map[string]any{"user_id": adminUser.ID, "username": adminUser.Username})

	// Auto-login admin user
	sessionData := &models.SessionData{
		UserID:                 adminUser.ID,
		Username:               adminUser.Username,
		Role:                   adminUser.Role,
		PasswordChangeRequired: false,
		ShareAuthenticated:     make(map[string]bool),
	}

	if h.cfg != nil && h.cfg.SecretKey != "" {
		_ = middleware.SetSessionCookie(w, sessionData, h.cfg.SecretKey, false, middleware.DefaultSessionMaxAge)
	}

	h.JSON(w, http.StatusOK, map[string]any{
		"status":   "ok",
		"role":     "admin",
		"redirect": "/",
	})
}

// APIChangePasswordHandler updates the password for the currently authenticated user.
func (h *Handlers) APIChangePasswordHandler(w http.ResponseWriter, r *http.Request) {
	var req models.ChangePasswordRequest
	if err := h.DecodeJSON(r, &req); err != nil {
		h.JSONError(w, http.StatusBadRequest, "validation_failed", "Invalid request body")
		return
	}

	if err := req.Validate(); err != nil {
		h.JSONError(w, http.StatusBadRequest, "validation_failed", err.Error())
		return
	}

	sess := h.GetSession(r)
	if sess == nil || !sess.IsAuthenticated() {
		h.JSONError(w, http.StatusUnauthorized, "unauthorized", "Authentication required")
		return
	}

	ctx := r.Context()
	user, err := h.db.GetUser(ctx, sess.UserID)
	if err != nil || user == nil {
		h.JSONError(w, http.StatusNotFound, "not_found", "User not found")
		return
	}

	if !security.CheckPasswordHash(req.CurrentPassword, user.PasswordHash) {
		h.JSONError(w, http.StatusBadRequest, "invalid_password", h.Translate(r, "current_password_incorrect"))
		return
	}

	newHash, err := security.HashPassword(req.NewPassword)
	if err != nil {
		h.JSONError(w, http.StatusInternalServerError, "internal_error", "Failed to hash new password")
		return
	}

	_, err = h.db.UpdateUser(ctx, user.ID, map[string]any{
		"password_hash":            newHash,
		"password_change_required": false,
	})
	if err != nil {
		h.JSONError(w, http.StatusInternalServerError, "internal_error", "Failed to update password")
		return
	}

	// Update session cookie with password_change_required = false
	sess.PasswordChangeRequired = false
	if h.cfg != nil && h.cfg.SecretKey != "" {
		_ = middleware.SetSessionCookie(w, sess, h.cfg.SecretKey, false, middleware.DefaultSessionMaxAge)
	}

	h.audit(r, "auth.change_password", map[string]any{"user_id": user.ID, "username": user.Username})

	h.JSONOK(w, map[string]any{"message": "Password updated"})
}

func generateCaptchaDigits(n int) string {
	digits := "0123456789"
	var sb strings.Builder
	for i := 0; i < n; i++ {
		idx, _ := rand.Int(rand.Reader, big.NewInt(int64(len(digits))))
		sb.WriteByte(digits[idx.Int64()])
	}
	return sb.String()
}

func generateCaptchaImage(text string) []byte {
	width, height := 160, 60
	img := image.NewRGBA(image.Rect(0, 0, width, height))

	// Background
	bg := color.RGBA{R: 245, G: 247, B: 250, A: 255}
	draw.Draw(img, img.Bounds(), &image.Uniform{C: bg}, image.Point{}, draw.Src)

	// Draw simple digit strokes
	fg := color.RGBA{R: 40, G: 60, B: 110, A: 255}
	dotColor := color.RGBA{R: 180, G: 190, B: 210, A: 255}

	// Add noise dots
	for x := 0; x < width; x += 6 {
		for y := 0; y < height; y += 6 {
			img.Set(x, y, dotColor)
		}
	}

	// Draw coarse representation of each character
	charWidth := width / (len(text) + 1)
	for i, c := range text {
		startX := (i + 1) * charWidth
		startY := height / 3

		// Draw simple symbol representation
		for dx := -4; dx <= 4; dx++ {
			for dy := -8; dy <= 8; dy++ {
				if (dx+dy+int(c))%3 == 0 {
					img.Set(startX+dx, startY+dy, fg)
					img.Set(startX+dx+1, startY+dy, fg)
				}
			}
		}
	}

	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}
