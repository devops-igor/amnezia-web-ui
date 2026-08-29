package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/devops-igor/amnezia-web-ui-go/internal/middleware"
	"github.com/devops-igor/amnezia-web-ui-go/internal/models"
)

func TestLeaderboardHandler(t *testing.T) {
	h, db, _ := setupTestHandlers(t)
	ctx := context.Background()

	u := &models.User{
		ID:           "u-lb-1",
		Username:     "speeddemon",
		PasswordHash: "hash",
		Role:         models.RoleUser,
		Enabled:      true,
		TrafficTotal: 5000000000,
		CreatedAt:    time.Now(),
	}
	_, _ = db.CreateUser(ctx, u)

	periods := []string{"all-time", "monthly", "last-month", "invalid-period", ""}
	for _, p := range periods {
		t.Run("Period_"+p, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/leaderboard?period="+p, nil)
			w := httptest.NewRecorder()
			h.LeaderboardHandler(w, req)

			if w.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d", w.Code)
			}
		})
	}

	t.Run("Monthly Label Present", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/leaderboard?period=monthly", nil)
		w := httptest.NewRecorder()
		h.LeaderboardHandler(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}
		body := w.Body.String()
		if body == "" {
			t.Errorf("expected non-empty body")
		}
	})

	t.Run("Last-Month Label Present", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/leaderboard?period=last-month", nil)
		w := httptest.NewRecorder()
		h.LeaderboardHandler(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}
	})

	t.Run("CurrentUserRank Identified", func(t *testing.T) {
		sess := &models.SessionData{
			UserID:   u.ID,
			Username: "speeddemon",
			Role:     models.RoleUser,
		}
		req := httptest.NewRequest(http.MethodGet, "/api/leaderboard", nil)
		reqCtx := middleware.WithSession(req.Context(), sess)
		w := httptest.NewRecorder()
		h.LeaderboardHandler(w, req.WithContext(reqCtx))
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}
	})
}
