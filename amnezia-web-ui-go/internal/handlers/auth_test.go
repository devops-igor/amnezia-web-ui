package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/devops-igor/amnezia-web-ui-go/internal/config"
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

func TestSetupRace_UniqueAdminConstraint(t *testing.T) {
	h, db, _ := setupTestHandlers(t)
	ctx := context.Background()

	// Clear any seeded users for fresh setup race
	users, _ := db.GetAllUsers(ctx)
	for _, u := range users {
		_, _ = db.DeleteUser(ctx, u.ID)
	}

	concurrentRequests := 10
	var successCount atomic.Int32
	var conflictCount atomic.Int32
	var wg sync.WaitGroup

	for i := 0; i < concurrentRequests; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			body, _ := json.Marshal(models.SetupRequest{
				Username:        fmt.Sprintf("admin_%d", idx),
				Password:        "AdminSecretPass123!",
				ConfirmPassword: "AdminSecretPass123!",
			})
			req := httptest.NewRequest(http.MethodPost, "/api/auth/setup", bytes.NewReader(body))
			w := httptest.NewRecorder()
			h.APISetupHandler(w, req)

			if w.Code == http.StatusOK {
				successCount.Add(1)
			} else if w.Code == http.StatusConflict {
				conflictCount.Add(1)
			}
		}(i)
	}
	wg.Wait()

	if successCount.Load() != 1 {
		t.Fatalf("expected exactly 1 successful setup, got %d (conflicts: %d)", successCount.Load(), conflictCount.Load())
	}
	if conflictCount.Load() != int32(concurrentRequests-1) {
		t.Errorf("expected %d conflict responses, got %d", concurrentRequests-1, conflictCount.Load())
	}

	userCount, err := db.CountUsers(ctx)
	if err != nil || userCount != 1 {
		t.Fatalf("expected exactly 1 admin user in DB, got count=%d, err=%v", userCount, err)
	}
}

func TestDisabledUser_SessionRejected(t *testing.T) {
	_, db, cfg := setupTestHandlers(t)
	ctx := context.Background()

	middleware.SetUserLookup(func(ctx context.Context, userID string) (*models.User, error) {
		return db.GetUser(ctx, userID)
	})
	t.Cleanup(func() {
		middleware.SetUserLookup(nil)
	})

	userPassHash, _ := security.HashPassword("TestPass123!")
	testUser := &models.User{
		ID:           "test-revoke-user-1",
		Username:     "revokeuser",
		PasswordHash: userPassHash,
		Role:         models.RoleUser,
		Enabled:      true,
		CreatedAt:    time.Now(),
	}
	_, err := db.CreateUser(ctx, testUser)
	if err != nil {
		t.Fatalf("failed to create test user: %v", err)
	}

	sessionData := &models.SessionData{
		UserID:   testUser.ID,
		Username: testUser.Username,
		Role:     testUser.Role,
	}
	encodedCookie, err := security.EncodeSession(sessionData.ToMap(), cfg.SecretKey)
	if err != nil {
		t.Fatalf("failed to encode session: %v", err)
	}

	okHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	})
	handler := middleware.Session(cfg.SecretKey)(middleware.RequireAuth(okHandler))

	// 1. Valid active user request -> 200 OK
	req1 := httptest.NewRequest(http.MethodGet, "/api/connections", nil)
	req1.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: encodedCookie})
	w1 := httptest.NewRecorder()
	handler.ServeHTTP(w1, req1)
	if w1.Code != http.StatusOK {
		t.Fatalf("expected 200 for active user, got %d", w1.Code)
	}

	// 2. Disable user in DB -> 401 Unauthorized
	_, err = db.UpdateUser(ctx, testUser.ID, map[string]any{"enabled": false})
	if err != nil {
		t.Fatalf("failed to disable user: %v", err)
	}

	req2 := httptest.NewRequest(http.MethodGet, "/api/connections", nil)
	req2.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: encodedCookie})
	w2 := httptest.NewRecorder()
	handler.ServeHTTP(w2, req2)
	if w2.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for disabled user session, got %d", w2.Code)
	}

	// 3. Delete user from DB -> 401 Unauthorized
	_, _ = db.DeleteUser(ctx, testUser.ID)
	req3 := httptest.NewRequest(http.MethodGet, "/api/connections", nil)
	req3.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: encodedCookie})
	w3 := httptest.NewRecorder()
	handler.ServeHTTP(w3, req3)
	if w3.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for deleted user session, got %d", w3.Code)
	}
}

func TestSetLang_Validation(t *testing.T) {
	h, _, _ := setupTestHandlers(t)

	tests := []struct {
		name         string
		langParam    string
		expectedLang string
	}{
		{"Valid RU", "ru", "ru"},
		{"Valid EN", "en", "en"},
		{"Path Traversal Attack", "../../../etc/passwd", "en"},
		{"Arbitrary Malicious String", "<script>alert(1)</script>", "en"},
		{"Overlong String", strings.Repeat("x", 2000), "en"},
		{"Empty Param", "", "en"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rctx := chi.NewRouteContext()
			rctx.URLParams.Add("lang", tc.langParam)
			req := httptest.NewRequest(http.MethodGet, "/set_lang", nil)
			req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
			req.Header.Set("Referer", "/my")
			w := httptest.NewRecorder()
			h.SetLangHandler(w, req)

			if w.Code != http.StatusFound {
				t.Fatalf("expected 302 redirect, got %d", w.Code)
			}

			cookies := w.Result().Cookies()
			var foundLang, foundPanelLang string
			for _, c := range cookies {
				if c.Name == "lang" {
					foundLang = c.Value
				}
				if c.Name == "panel_lang" {
					foundPanelLang = c.Value
				}
			}

			if foundLang != tc.expectedLang {
				t.Errorf("expected lang cookie %q, got %q", tc.expectedLang, foundLang)
			}
			if foundPanelLang != tc.expectedLang {
				t.Errorf("expected panel_lang cookie %q, got %q", tc.expectedLang, foundPanelLang)
			}
		})
	}
}

func TestEmptySecretKey_ReturnsError(t *testing.T) {
	h, _, _ := setupTestHandlers(t)
	h.cfg = &config.Config{
		SecretKey: "",
	}

	// 1. CaptchaHandler fast-fails with 500 when SecretKey is empty
	reqCaptcha := httptest.NewRequest(http.MethodGet, "/api/auth/captcha", nil)
	wCaptcha := httptest.NewRecorder()
	h.CaptchaHandler(wCaptcha, reqCaptcha)

	if wCaptcha.Code != http.StatusInternalServerError {
		t.Errorf("expected 500 for Captcha with empty SecretKey, got %d", wCaptcha.Code)
	}
	if !strings.Contains(wCaptcha.Body.String(), "Session signing key not configured") {
		t.Errorf("expected detail 'Session signing key not configured', got %s", wCaptcha.Body.String())
	}

	// 2. APILoginHandler fast-fails with 500 when SecretKey is empty
	bodyLogin, _ := json.Marshal(models.LoginRequest{
		Username: "admin",
		Password: "AdminPassword123!",
	})
	reqLogin := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(bodyLogin))
	wLogin := httptest.NewRecorder()
	h.APILoginHandler(wLogin, reqLogin)

	if wLogin.Code != http.StatusInternalServerError {
		t.Errorf("expected 500 for APILogin with empty SecretKey, got %d", wLogin.Code)
	}
	if !strings.Contains(wLogin.Body.String(), "Session signing key not configured") {
		t.Errorf("expected detail 'Session signing key not configured', got %s", wLogin.Body.String())
	}
}
