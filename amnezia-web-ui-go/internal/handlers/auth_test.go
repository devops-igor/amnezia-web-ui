package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/devops-igor/amnezia-web-ui-go/internal/middleware"
	"github.com/devops-igor/amnezia-web-ui-go/internal/models"
	"github.com/devops-igor/amnezia-web-ui-go/internal/security"
	"github.com/go-chi/chi/v5"
)

func TestAuthHandlers(t *testing.T) {
	h, db, cfg := setupTestHandlers(t)
	ctx := context.Background()

	// Seed an admin and regular user
	adminPassHash, _ := security.HashPassword("AdminPass123!")
	adminUser := &models.User{
		ID:           "admin-1",
		Username:     "admin",
		PasswordHash: adminPassHash,
		Role:         models.RoleAdmin,
		Enabled:      true,
		CreatedAt:    time.Now(),
	}
	_, _ = db.CreateUser(ctx, adminUser)

	userPassHash, _ := security.HashPassword("UserPass123!")
	normalUser := &models.User{
		ID:           "user-1",
		Username:     "regularuser",
		PasswordHash: userPassHash,
		Role:         models.RoleUser,
		Enabled:      true,
		CreatedAt:    time.Now(),
	}
	_, _ = db.CreateUser(ctx, normalUser)

	disabledUser := &models.User{
		ID:           "user-disabled",
		Username:     "disableduser",
		PasswordHash: userPassHash,
		Role:         models.RoleUser,
		Enabled:      false,
		CreatedAt:    time.Now(),
	}
	_, _ = db.CreateUser(ctx, disabledUser)

	t.Run("LoginHandler Unauthenticated", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/login", nil)
		w := httptest.NewRecorder()
		h.LoginHandler(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", w.Code)
		}
	})

	t.Run("LoginHandler Authenticated Admin", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/login", nil)
		reqCtx := middleware.WithSession(req.Context(), &models.SessionData{
			UserID: "admin-1",
			Role:   models.RoleAdmin,
		})
		w := httptest.NewRecorder()
		h.LoginHandler(w, req.WithContext(reqCtx))
		if w.Code != http.StatusFound || w.Header().Get("Location") != "/" {
			t.Errorf("expected 302 redirect to /, got %d (%s)", w.Code, w.Header().Get("Location"))
		}
	})

	t.Run("LoginHandler Authenticated User", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/login", nil)
		reqCtx := middleware.WithSession(req.Context(), &models.SessionData{
			UserID: "user-1",
			Role:   models.RoleUser,
		})
		w := httptest.NewRecorder()
		h.LoginHandler(w, req.WithContext(reqCtx))
		if w.Code != http.StatusFound || w.Header().Get("Location") != "/my" {
			t.Errorf("expected 302 redirect to /my, got %d (%s)", w.Code, w.Header().Get("Location"))
		}
	})

	t.Run("LogoutHandler", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/logout", nil)
		w := httptest.NewRecorder()
		h.LogoutHandler(w, req)
		if w.Code != http.StatusFound || w.Header().Get("Location") != "/login" {
			t.Errorf("expected 302 redirect to /login, got %d", w.Code)
		}
	})

	t.Run("SetLangHandler", func(t *testing.T) {
		r := chi.NewRouter()
		r.Get("/set_lang/{lang}", h.SetLangHandler)

		req := httptest.NewRequest(http.MethodGet, "/set_lang/ru", nil)
		req.Header.Set("Referer", "/server/1")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusFound || w.Header().Get("Location") != "/server/1" {
			t.Errorf("expected 302 redirect to /server/1, got %d (%s)", w.Code, w.Header().Get("Location"))
		}
	})

	t.Run("CaptchaHandler JSON and Image", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/auth/captcha", nil)
		w := httptest.NewRecorder()
		h.CaptchaHandler(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", w.Code)
		}

		reqImg := httptest.NewRequest(http.MethodGet, "/api/auth/captcha", nil)
		reqImg.Header.Set("Accept", "image/png")
		wImg := httptest.NewRecorder()
		h.CaptchaHandler(wImg, reqImg)
		if wImg.Code != http.StatusOK || wImg.Header().Get("Content-Type") != "image/png" {
			t.Errorf("expected 200 image/png, got %d (%s)", wImg.Code, wImg.Header().Get("Content-Type"))
		}
	})

	t.Run("APILoginHandler Success Admin", func(t *testing.T) {
		body, _ := json.Marshal(models.LoginRequest{
			Username: "admin",
			Password: "AdminPass123!",
		})
		req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(body))
		w := httptest.NewRecorder()
		h.APILoginHandler(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d (body: %s)", w.Code, w.Body.String())
		}
	})

	t.Run("APILoginHandler Success User", func(t *testing.T) {
		body, _ := json.Marshal(models.LoginRequest{
			Username: "regularuser",
			Password: "UserPass123!",
		})
		req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(body))
		w := httptest.NewRecorder()
		h.APILoginHandler(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d (body: %s)", w.Code, w.Body.String())
		}
	})

	t.Run("APILoginHandler Invalid Credentials", func(t *testing.T) {
		body, _ := json.Marshal(models.LoginRequest{
			Username: "admin",
			Password: "WrongPassword!",
		})
		req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(body))
		w := httptest.NewRecorder()
		h.APILoginHandler(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", w.Code)
		}
	})

	t.Run("APILoginHandler Disabled Account", func(t *testing.T) {
		body, _ := json.Marshal(models.LoginRequest{
			Username: "disableduser",
			Password: "UserPass123!",
		})
		req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(body))
		w := httptest.NewRecorder()
		h.APILoginHandler(w, req)

		if w.Code != http.StatusForbidden {
			t.Errorf("expected 403, got %d", w.Code)
		}
	})

	t.Run("APILoginHandler Captcha Verification", func(t *testing.T) {
		_ = db.SetSetting(ctx, "captcha", models.CaptchaSettings{Enabled: true})
		defer func() {
			_ = db.SetSetting(ctx, "captcha", models.CaptchaSettings{Enabled: false})
		}()

		captcha := "1234"
		sess := &models.SessionData{
			CaptchaAnswer: captcha,
		}

		// Wrong captcha
		badCaptcha := "9999"
		body, _ := json.Marshal(models.LoginRequest{
			Username: "admin",
			Password: "AdminPass123!",
			Captcha:  &badCaptcha,
		})
		req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(body))
		reqCtx := middleware.WithSession(req.Context(), sess)
		w := httptest.NewRecorder()
		h.APILoginHandler(w, req.WithContext(reqCtx))
		if w.Code != http.StatusBadRequest {
			t.Errorf("expected 400 for wrong captcha, got %d", w.Code)
		}

		// Correct captcha
		sess.CaptchaAnswer = captcha
		bodyOK, _ := json.Marshal(models.LoginRequest{
			Username: "admin",
			Password: "AdminPass123!",
			Captcha:  &captcha,
		})
		reqOK := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(bodyOK))
		reqOKCtx := middleware.WithSession(reqOK.Context(), sess)
		wOK := httptest.NewRecorder()
		h.APILoginHandler(wOK, reqOK.WithContext(reqOKCtx))
		if wOK.Code != http.StatusOK {
			t.Errorf("expected 200 for correct captcha, got %d", wOK.Code)
		}
	})

	t.Run("APISetupHandler Already Done", func(t *testing.T) {
		body, _ := json.Marshal(models.SetupRequest{
			Username:        "admin2",
			Password:        "Password123!",
			ConfirmPassword: "Password123!",
		})
		req := httptest.NewRequest(http.MethodPost, "/api/auth/setup", bytes.NewReader(body))
		w := httptest.NewRecorder()
		h.APISetupHandler(w, req)

		if w.Code != http.StatusConflict {
			t.Errorf("expected 409 setup already done, got %d", w.Code)
		}
	})

	t.Run("APISetupHandler Validation Errors", func(t *testing.T) {
		// Empty body
		reqEmpty := httptest.NewRequest(http.MethodPost, "/api/auth/setup", nil)
		wEmpty := httptest.NewRecorder()
		h.APISetupHandler(wEmpty, reqEmpty)
		if wEmpty.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", wEmpty.Code)
		}

		// Password mismatch
		bodyMismatch, _ := json.Marshal(models.SetupRequest{
			Username:        "admin3",
			Password:        "Password123!",
			ConfirmPassword: "DifferentPassword123!",
		})
		reqMismatch := httptest.NewRequest(http.MethodPost, "/api/auth/setup", bytes.NewReader(bodyMismatch))
		wMismatch := httptest.NewRecorder()
		h.APISetupHandler(wMismatch, reqMismatch)
		if wMismatch.Code != http.StatusBadRequest {
			t.Errorf("expected 400 for mismatch, got %d", wMismatch.Code)
		}
	})

	t.Run("APIChangePasswordHandler Validation Failures", func(t *testing.T) {
		sess := &models.SessionData{
			UserID: "user-1",
			Role:   models.RoleUser,
		}

		// Empty body
		reqEmpty := httptest.NewRequest(http.MethodPost, "/api/auth/change-password", nil)
		reqEmptyCtx := middleware.WithSession(reqEmpty.Context(), sess)
		wEmpty := httptest.NewRecorder()
		h.APIChangePasswordHandler(wEmpty, reqEmpty.WithContext(reqEmptyCtx))
		if wEmpty.Code != http.StatusBadRequest {
			t.Errorf("expected 400 for empty body, got %d", wEmpty.Code)
		}

		// Password mismatch
		bodyMismatch, _ := json.Marshal(models.ChangePasswordRequest{
			CurrentPassword: "UserPass123!",
			NewPassword:     "NewPassword123!",
			ConfirmPassword: "MismatchPassword123!",
		})
		reqMismatch := httptest.NewRequest(http.MethodPost, "/api/auth/change-password", bytes.NewReader(bodyMismatch))
		reqMismatchCtx := middleware.WithSession(reqMismatch.Context(), sess)
		wMismatch := httptest.NewRecorder()
		h.APIChangePasswordHandler(wMismatch, reqMismatch.WithContext(reqMismatchCtx))
		if wMismatch.Code != http.StatusBadRequest {
			t.Errorf("expected 400 for password mismatch, got %d", wMismatch.Code)
		}
	})

	t.Run("APIChangePasswordHandler", func(t *testing.T) {
		// 1. Unauthenticated
		body, _ := json.Marshal(models.ChangePasswordRequest{
			CurrentPassword: "UserPass123!",
			NewPassword:     "NewUserPass123!",
			ConfirmPassword: "NewUserPass123!",
		})
		reqUnauth := httptest.NewRequest(http.MethodPost, "/api/auth/change-password", bytes.NewReader(body))
		wUnauth := httptest.NewRecorder()
		h.APIChangePasswordHandler(wUnauth, reqUnauth)
		if wUnauth.Code != http.StatusUnauthorized {
			t.Errorf("expected 401 unauthenticated, got %d", wUnauth.Code)
		}

		// 2. Wrong current password
		sess := &models.SessionData{
			UserID: "user-1",
			Role:   models.RoleUser,
		}
		bodyWrong, _ := json.Marshal(models.ChangePasswordRequest{
			CurrentPassword: "WrongOldPassword!",
			NewPassword:     "NewUserPass123!",
			ConfirmPassword: "NewUserPass123!",
		})
		reqWrong := httptest.NewRequest(http.MethodPost, "/api/auth/change-password", bytes.NewReader(bodyWrong))
		reqWrongCtx := middleware.WithSession(reqWrong.Context(), sess)
		wWrong := httptest.NewRecorder()
		h.APIChangePasswordHandler(wWrong, reqWrong.WithContext(reqWrongCtx))
		if wWrong.Code != http.StatusBadRequest {
			t.Errorf("expected 400 wrong current password, got %d", wWrong.Code)
		}

		// 3. Success
		reqSuccess := httptest.NewRequest(http.MethodPost, "/api/auth/change-password", bytes.NewReader(body))
		reqSuccessCtx := middleware.WithSession(reqSuccess.Context(), sess)
		wSuccess := httptest.NewRecorder()
		h.APIChangePasswordHandler(wSuccess, reqSuccess.WithContext(reqSuccessCtx))
		if wSuccess.Code != http.StatusOK {
			t.Errorf("expected 200 password updated, got %d (body: %s)", wSuccess.Code, wSuccess.Body.String())
		}
	})

	t.Run("APISetupHandler First Run Success", func(t *testing.T) {
		hEmpty, dbEmpty, _ := setupTestHandlers(t)
		body, _ := json.Marshal(models.SetupRequest{
			Username:        "superadmin",
			Password:        "SuperSecret123!",
			ConfirmPassword: "SuperSecret123!",
		})
		req := httptest.NewRequest(http.MethodPost, "/api/auth/setup", bytes.NewReader(body))
		w := httptest.NewRecorder()
		hEmpty.APISetupHandler(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 setup success on empty db, got %d (body: %s)", w.Code, w.Body.String())
		}

		// Verify admin persisted with admin role
		admin, err := dbEmpty.GetUserByUsername(ctx, "superadmin")
		if err != nil || admin == nil {
			t.Fatalf("expected admin user created")
		}
		if admin.Role != models.RoleAdmin {
			t.Errorf("expected admin role, got %s", admin.Role)
		}

		// Second setup attempt now conflicts
		req2 := httptest.NewRequest(http.MethodPost, "/api/auth/setup", bytes.NewReader(body))
		w2 := httptest.NewRecorder()
		hEmpty.APISetupHandler(w2, req2)
		if w2.Code != http.StatusConflict {
			t.Errorf("expected 409 on second setup, got %d", w2.Code)
		}

		// Bad JSON
		reqBad := httptest.NewRequest(http.MethodPost, "/api/auth/setup", bytes.NewReader([]byte("bad")))
		wBad := httptest.NewRecorder()
		hEmpty.APISetupHandler(wBad, reqBad)
		if wBad.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", wBad.Code)
		}

		// Validation failure
		bodyInvalid, _ := json.Marshal(models.SetupRequest{
			Username:        "ab",
			Password:        "short",
			ConfirmPassword: "short",
		})
		reqInvalid := httptest.NewRequest(http.MethodPost, "/api/auth/setup", bytes.NewReader(bodyInvalid))
		wInvalid := httptest.NewRecorder()
		hEmpty.APISetupHandler(wInvalid, reqInvalid)
		if wInvalid.Code != http.StatusBadRequest {
			t.Errorf("expected 400 for invalid setup body, got %d", wInvalid.Code)
		}

		// Nil DB
		hNilDB := &Handlers{}
		reqNilDB := httptest.NewRequest(http.MethodPost, "/api/auth/setup", bytes.NewReader(body))
		wNilDB := httptest.NewRecorder()
		hNilDB.APISetupHandler(wNilDB, reqNilDB)
		if wNilDB.Code != http.StatusInternalServerError {
			t.Errorf("expected 500 for nil DB, got %d", wNilDB.Code)
		}
	})

	t.Run("APIChangePasswordHandler User Not Found", func(t *testing.T) {
		sessNF := &models.SessionData{UserID: "ghost-user", Role: models.RoleUser}
		body, _ := json.Marshal(models.ChangePasswordRequest{
			CurrentPassword: "old",
			NewPassword:     "NewPass123!",
			ConfirmPassword: "NewPass123!",
		})
		reqNF := httptest.NewRequest(http.MethodPost, "/api/auth/change-password", bytes.NewReader(body))
		reqNFCtx := middleware.WithSession(reqNF.Context(), sessNF)
		wNF := httptest.NewRecorder()
		h.APIChangePasswordHandler(wNF, reqNF.WithContext(reqNFCtx))
		if wNF.Code != http.StatusNotFound {
			t.Errorf("expected 404 for ghost user change-password, got %d", wNF.Code)
		}
	})

	_ = cfg
}
