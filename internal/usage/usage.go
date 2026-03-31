package usage

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"codex-switch/internal/config"
	"codex-switch/internal/support"
)

func Fetch(client *http.Client, cfg config.Config, accessToken string) (map[string]any, error) {
	if strings.TrimSpace(accessToken) == "" {
		return nil, fmt.Errorf("missing access_token")
	}

	requestCtx, cancel := context.WithTimeout(context.Background(), cfg.UsageTimeoutDuration())
	defer cancel()

	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, cfg.Network.UsageURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "codex-switch")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		detail := strings.TrimSpace(string(body))
		if detail == "" {
			detail = resp.Status
		}
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, detail)
	}

	result := map[string]any{}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("invalid json response: %w", err)
	}

	return result, nil
}

func RemainingPercentFromWindow(window map[string]any) *int {
	used, ok := floatField(window, "used_percent")
	if !ok {
		return nil
	}

	normalized := max(0, min(100, int(used+0.999999)))
	remaining := 100 - normalized
	return &remaining
}

func SummarizeWindow(window map[string]any, now time.Time) (string, string) {
	if len(window) == 0 {
		return "-", "-"
	}

	remaining := RemainingPercentFromWindow(window)
	usageValue := "-"
	if remaining != nil {
		usageValue = fmt.Sprintf("%d%% left", *remaining)
	}

	return usageValue, support.FormatUnix(unixField(window, "reset_at"), now)
}

func SummarizeRateLimit(rateLimit map[string]any, now time.Time) (string, string, string, string) {
	if len(rateLimit) == 0 {
		return "-", "-", "-", "-"
	}

	primary := mapField(rateLimit, "primary_window")
	secondary := mapField(rateLimit, "secondary_window")

	primaryValue, primaryReset := SummarizeWindow(primary, now)
	secondaryValue, secondaryReset := SummarizeWindow(secondary, now)
	return primaryValue, primaryReset, secondaryValue, secondaryReset
}

func SummarizeReviewLimit(rateLimit map[string]any) string {
	if len(rateLimit) == 0 {
		return "-"
	}

	allowed := boolField(rateLimit, "allowed")
	limitReached := boolField(rateLimit, "limit_reached")
	primary := mapField(rateLimit, "primary_window")
	used, ok := floatField(primary, "used_percent")

	if limitReached || !allowed {
		if !ok {
			return "blocked"
		}
		return fmt.Sprintf("blocked %.0f%%", used)
	}
	if !ok {
		return "open"
	}
	return fmt.Sprintf("open %.0f%%", used)
}

func mapField(payload map[string]any, key string) map[string]any {
	value, ok := payload[key].(map[string]any)
	if !ok {
		return map[string]any{}
	}
	return value
}

func unixField(payload map[string]any, key string) *int64 {
	value, ok := floatField(payload, key)
	if !ok {
		return nil
	}
	parsed := int64(value)
	return &parsed
}

func floatField(payload map[string]any, key string) (float64, bool) {
	value, ok := payload[key].(float64)
	return value, ok
}

func boolField(payload map[string]any, key string) bool {
	value, _ := payload[key].(bool)
	return value
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
