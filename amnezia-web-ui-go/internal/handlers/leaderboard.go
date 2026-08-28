package handlers

import (
	"net/http"
	"time"

	"github.com/devops-igor/amnezia-web-ui-go/internal/models"
)

// LeaderboardHandler aggregates traffic metrics and returns the ranked user leaderboard.
func (h *Handlers) LeaderboardHandler(w http.ResponseWriter, r *http.Request) {
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
	entries, err := h.db.GetLeaderboard(ctx, period)
	if err != nil {
		h.JSONError(w, http.StatusInternalServerError, "internal_error", "Failed to retrieve leaderboard")
		return
	}

	entryResponses := make([]models.LeaderboardEntryResponse, len(entries))
	var currentUserRank *int

	sess := h.GetSession(r)
	for i, e := range entries {
		entryResponses[i] = models.LeaderboardEntryResponse{
			Rank:     e.Rank,
			Username: e.Username,
			Download: e.Download,
			Upload:   e.Upload,
			Total:    e.Total,
		}
		if sess != nil && sess.Username == e.Username {
			rankVal := e.Rank
			currentUserRank = &rankVal
		}
	}

	h.JSON(w, http.StatusOK, models.LeaderboardResponse{
		Period:          period,
		Entries:         entryResponses,
		CurrentUserRank: currentUserRank,
		MonthlyLabel:    monthlyLabel,
	})
}
