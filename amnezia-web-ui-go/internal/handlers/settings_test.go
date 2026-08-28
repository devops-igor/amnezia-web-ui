package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/devops-igor/amnezia-web-ui-go/internal/models"
)

func TestSettingsHandlers(t *testing.T) {
	h, db, _ := setupTestHandlers(t)
	ctx := context.Background()

	_ = db.SetSetting(ctx, "ssl", models.SSLSettings{
		KeyText:  "secret-key",
		CertText: "secret-cert",
	})

	r := setupFullSettingsRouter(h)

	t.Run("GetSettingsHandler Masks Secrets", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}

		var resp map[string]any
		_ = json.NewDecoder(w.Body).Decode(&resp)
		if ssl, ok := resp["ssl"].(map[string]any); ok {
			if ssl["key_text"] != "" || ssl["cert_text"] != "" {
				t.Errorf("expected masked ssl key and cert text")
			}
		}
	})

	t.Run("SaveSettingsHandler", func(t *testing.T) {
		body, _ := json.Marshal(models.SaveSettingsRequest{
			Appearance: models.AppearanceSettings{Title: "Amnezia Pro"},
			Sync:       models.SyncSettings{RemnawaveSync: false},
			Captcha:    models.CaptchaSettings{Enabled: false},
			SSL:        models.SSLSettings{},
			Limits:     models.ConnectionLimits{MaxConnectionsPerUser: 15},
		})
		req := httptest.NewRequest(http.MethodPost, "/api/settings/save", bytes.NewReader(body))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}
	})

	t.Run("SyncNowHandler", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/settings/sync_now", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}
	})

	t.Run("SaveSettingsHandler With Telegram", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{
			"appearance": map[string]any{"title": "TG Panel"},
			"sync":       map[string]any{"remnawave_sync": false},
			"captcha":    map[string]any{"enabled": false},
			"ssl":        map[string]any{},
			"limits":     map[string]any{"max_connections_per_user": 20},
			"telegram":   map[string]any{"bot_token": "123:abc", "chat_id": "456"},
		})
		req := httptest.NewRequest(http.MethodPost, "/api/settings/save", bytes.NewReader(body))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}

		// Verify telegram setting persisted
		var tg map[string]any
		_ = db.GetSetting(ctx, "telegram", &tg)
		if tg == nil || tg["bot_token"] != "123:abc" {
			t.Errorf("expected telegram settings persisted, got %v", tg)
		}
	})

	t.Run("SyncNowHandler With Pending Users", func(t *testing.T) {
		// Seed a RemnaWave-linked user
		rwUUID := "rw-pending-2"
		uRW := &models.User{
			ID:            "rw-pending-u2",
			Username:      "rwpending2",
			PasswordHash:  "hash",
			Role:          models.RoleUser,
			Enabled:       true,
			RemnaWaveUUID: &rwUUID,
			CreatedAt:     time.Now(),
		}
		_, _ = db.CreateUser(ctx, uRW)

		req := httptest.NewRequest(http.MethodPost, "/api/settings/sync_now", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}
	})

	t.Run("SyncDeleteHandler", func(t *testing.T) {
		rwUUID := "remnawave-uuid-1"
		u := &models.User{
			ID:            "u-rw-1",
			Username:      "rwuser",
			PasswordHash:  "hash",
			Role:          models.RoleUser,
			RemnaWaveUUID: &rwUUID,
		}
		_, _ = db.CreateUser(ctx, u)

		req := httptest.NewRequest(http.MethodPost, "/api/settings/sync_delete", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}
	})

	t.Run("DownloadBackupHandler", func(t *testing.T) {
		// Seed data so backup has content
		sBk := &models.Server{Name: "Backup-Server", Host: "1.2.3.4", SSHUser: "root", CreatedAt: time.Now()}
		sBkID, _ := db.CreateServer(ctx, sBk)
		uBk := &models.User{ID: "bk-user", Username: "bkuser", PasswordHash: "hash", Role: models.RoleUser, CreatedAt: time.Now()}
		_, _ = db.CreateUser(ctx, uBk)
		cBk := &models.UserConnection{ID: "bk-conn", UserID: uBk.ID, ServerID: sBkID, Protocol: "awg", ClientID: "bk-client", Name: "BK", CreatedAt: time.Now()}
		_, _ = db.CreateConnection(ctx, cBk)

		req := httptest.NewRequest(http.MethodGet, "/api/settings/backup/download", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}
		if w.Header().Get("Content-Type") != "application/json" {
			t.Errorf("expected application/json, got %s", w.Header().Get("Content-Type"))
		}

		body := w.Body.String()
		if !strings.Contains(body, "Backup-Server") || !strings.Contains(body, "bkuser") || !strings.Contains(body, "bk-conn") {
			t.Errorf("expected backup to contain seeded entities")
		}
		if !strings.Contains(body, "Content-Disposition") && w.Header().Get("Content-Disposition") == "" {
			t.Logf("no content-disposition header (informational)")
		}
	})

	t.Run("RestoreBackupHandler Full JSON", func(t *testing.T) {
		nowStr := time.Now().Format(time.RFC3339)
		backup := models.BackupData{
			Settings: map[string]any{
				"appearance": map[string]any{"title": "Restored Amnezia"},
			},
			Servers: []map[string]any{
				{
					"id":        float64(10),
					"name":      "Restored Server",
					"host":      "10.0.0.99",
					"ssh_user":  "root",
					"ssh_port":  float64(22),
					"protocols": map[string]any{"awg": map[string]any{"installed": true}},
				},
			},
			Users: []map[string]any{
				{
					"id":                     "restored-u-1",
					"username":               "restoreduser",
					"role":                   "user",
					"enabled":                true,
					"email":                  "restored@example.com",
					"telegramId":             "9999",
					"description":            "restored desc",
					"share_token":            "stoken",
					"remnawave_uuid":         "rw-123",
					"created_at":             nowStr,
					"traffic_limit":          float64(5000),
					"traffic_reset_strategy": "monthly",
					"share_enabled":          true,
					"awg_mimicry":            "auto",
				},
			},
			UserConnections: []map[string]any{
				{
					"id":          "restored-c-1",
					"user_id":     "restored-u-1",
					"server_id":   float64(10),
					"protocol":    "awg",
					"client_id":   "restored-client-1",
					"name":        "Restored Phone",
					"created_at":  nowStr,
					"awg_mimicry": "auto",
				},
			},
		}
		body, _ := json.Marshal(backup)
		req := httptest.NewRequest(http.MethodPost, "/api/settings/backup/restore", bytes.NewReader(body))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d (body: %s)", w.Code, w.Body.String())
		}

		// Assert the restored entity counts programmatically.
		var resp struct {
			Status   string         `json:"status"`
			Restored map[string]int `json:"restored"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to decode restore response: %v (body: %s)", err, w.Body.String())
		}
		if resp.Status != "ok" {
			t.Errorf("expected status ok, got %q", resp.Status)
		}
		want := map[string]int{"servers": 1, "users": 1, "conns": 1, "settings": 1}
		for k, v := range want {
			if got := resp.Restored[k]; got != v {
				t.Errorf("restored[%s] = %d, want %d", k, got, v)
			}
		}
	})

	t.Run("RestoreBackupHandler Multipart", func(t *testing.T) {
		buf := new(bytes.Buffer)
		mw := multipart.NewWriter(buf)
		fw, _ := mw.CreateFormFile("file", "backup.json")
		_, _ = fw.Write([]byte(`{"settings": {"appearance": {"title": "Multipart Restored"}}}`))
		_ = mw.Close()

		req := httptest.NewRequest(http.MethodPost, "/api/settings/backup/restore", buf)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}
	})

	t.Run("SaveSettingsHandler Invalid JSON", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/settings/save", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", w.Code)
		}
	})

	t.Run("RestoreBackupHandler Invalid & Empty JSON", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/settings/backup/restore", bytes.NewReader([]byte("not-json")))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", w.Code)
		}

		reqEmpty := httptest.NewRequest(http.MethodPost, "/api/settings/backup/restore", bytes.NewReader([]byte("")))
		wEmpty := httptest.NewRecorder()
		r.ServeHTTP(wEmpty, reqEmpty)
		if wEmpty.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", wEmpty.Code)
		}
	})

	t.Run("Helper Functions strVal int64Val boolVal mapFromInterface", func(t *testing.T) {
		if strVal(123) != "" || strVal("hello") != "hello" {
			t.Errorf("strVal failed")
		}
		if int64Val(int64(42)) != 42 || int64Val(float64(42)) != 42 || int64Val(int(42)) != 42 || int64Val("42") != 42 || int64Val(nil) != 0 {
			t.Errorf("int64Val failed")
		}
		if intVal("50") != 50 {
			t.Errorf("intVal failed")
		}
		if !boolVal(true) || boolVal(false) || !boolVal("true") || !boolVal("1") || boolVal("false") || !boolVal(float64(1)) || boolVal(float64(0)) || boolVal(nil) {
			t.Errorf("boolVal failed")
		}
		if m := mapFromInterface(map[string]any{"a": "b"}); m["a"] != "b" {
			t.Errorf("mapFromInterface failed")
		}
		if m := mapFromInterface(nil); len(m) != 0 {
			t.Errorf("mapFromInterface nil failed")
		}
	})
}
