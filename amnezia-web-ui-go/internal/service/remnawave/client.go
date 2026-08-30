package remnawave

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// User represents an individual user record returned by the RemnaWave REST API.
type User struct {
	UUID        string `json:"uuid"`
	Username    string `json:"username"`
	Status      string `json:"status"`
	TelegramID  any    `json:"telegramId,omitempty"`
	Email       string `json:"email,omitempty"`
	Description string `json:"description,omitempty"`
}

// TelegramIDString safely converts the generic telegramId representation to a string pointer without scientific notation corruption.
func (u *User) TelegramIDString() *string {
	if u.TelegramID == nil {
		return nil
	}
	switch v := u.TelegramID.(type) {
	case string:
		s := strings.TrimSpace(v)
		if s == "" || s == "<nil>" {
			return nil
		}
		return &s
	case int64:
		s := strconv.FormatInt(v, 10)
		return &s
	case int:
		s := strconv.Itoa(v)
		return &s
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return nil
		}
		if v == math.Trunc(v) {
			s := strconv.FormatInt(int64(v), 10)
			return &s
		}
		s := strconv.FormatFloat(v, 'f', -1, 64)
		return &s
	case json.Number:
		s := v.String()
		if s == "" {
			return nil
		}
		return &s
	default:
		s := fmt.Sprint(v)
		if s == "" || s == "<nil>" {
			return nil
		}
		return &s
	}
}

type remnawaveResponseContainer struct {
	Response struct {
		Users []User `json:"users"`
		Total int    `json:"total"`
	} `json:"response"`
}

// HTTPClient defines the client interface for RemnaWave API operations.
type HTTPClient interface {
	GetUsers(ctx context.Context, baseURL, apiKey string, pageSize int) ([]User, error)
}

// Client implements HTTPClient with Bearer auth, timeouts, and exponential backoff retries.
type Client struct {
	httpClient *http.Client
	maxRetries int
	baseDelay  time.Duration
}

// ClientOption configures RemnaWave HTTP client options.
type ClientOption func(*Client)

// WithHTTPClient overrides the default http.Client.
func WithHTTPClient(httpClient *http.Client) ClientOption {
	return func(c *Client) {
		if httpClient != nil {
			c.httpClient = httpClient
		}
	}
}

// WithMaxRetries configures the maximum number of retry attempts.
func WithMaxRetries(retries int) ClientOption {
	return func(c *Client) {
		if retries >= 0 {
			c.maxRetries = retries
		}
	}
}

// WithBaseDelay configures the initial retry delay.
func WithBaseDelay(delay time.Duration) ClientOption {
	return func(c *Client) {
		if delay > 0 {
			c.baseDelay = delay
		}
	}
}

// NewClient creates a new configured RemnaWave HTTP API client.
func NewClient(opts ...ClientOption) *Client {
	c := &Client{
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		maxRetries: 3,
		baseDelay:  200 * time.Millisecond,
	}

	for _, opt := range opts {
		opt(c)
	}

	return c
}

// GetUsers retrieves all users from RemnaWave across paginated pages.
func (c *Client) GetUsers(ctx context.Context, baseURL, apiKey string, pageSize int) ([]User, error) {
	if baseURL == "" {
		return nil, errors.New("remnawave base url cannot be empty")
	}
	if apiKey == "" {
		return nil, errors.New("remnawave api key cannot be empty")
	}
	if pageSize <= 0 {
		pageSize = 50
	}

	endpoint := strings.TrimRight(baseURL, "/") + "/api/users"
	var allUsers []User
	start := 0

	for {
		users, total, err := c.fetchUserPageWithRetry(ctx, endpoint, apiKey, pageSize, start)
		if err != nil {
			return nil, err
		}

		allUsers = append(allUsers, users...)
		start += len(users)

		slog.Info("Fetched users from RemnaWave", "count", len(allUsers), "total", total)

		if start >= total || len(users) == 0 {
			break
		}
	}

	return allUsers, nil
}

func (c *Client) fetchUserPageWithRetry(ctx context.Context, endpoint, apiKey string, size, start int) ([]User, int, error) {
	pageURL := fmt.Sprintf("%s?size=%d&start=%d", endpoint, size, start)

	var lastErr error
	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		if attempt > 0 {
			backoff := c.baseDelay * time.Duration(math.Pow(2, float64(attempt-1)))
			select {
			case <-ctx.Done():
				return nil, 0, ctx.Err()
			case <-time.After(backoff):
			}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL, nil)
		if err != nil {
			return nil, 0, fmt.Errorf("failed to create request: %w", err)
		}

		req.Header.Set("Authorization", "Bearer "+apiKey)
		req.Header.Set("Accept", "application/json")

		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = err
			slog.Warn("RemnaWave request attempt failed", "attempt", attempt+1, "err", err)
			continue
		}

		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			lastErr = err
			continue
		}

		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("remnawave API error: %d %s", resp.StatusCode, string(body))
			if resp.StatusCode >= 400 && resp.StatusCode < 500 && resp.StatusCode != http.StatusTooManyRequests {
				// Non-retryable client error
				return nil, 0, lastErr
			}
			continue
		}

		var payload remnawaveResponseContainer
		dec := json.NewDecoder(bytes.NewReader(body))
		dec.UseNumber()
		if err := dec.Decode(&payload); err != nil {
			return nil, 0, fmt.Errorf("failed to decode remnawave response: %w", err)
		}

		return payload.Response.Users, payload.Response.Total, nil
	}

	return nil, 0, fmt.Errorf("remnawave request failed after %d attempts: %w", c.maxRetries+1, lastErr)
}
