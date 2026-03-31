package usage

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"codex-switch/internal/config"
)

func TestSummarizeRateLimit(t *testing.T) {
	t.Parallel()

	result := map[string]any{
		"primary_window": map[string]any{
			"used_percent": float64(100),
			"reset_at":     float64(1774867998),
		},
		"secondary_window": map[string]any{
			"used_percent": float64(53),
			"reset_at":     float64(1775211740),
		},
	}

	fiveHour, _, weekly, _ := SummarizeRateLimit(result, time.Unix(1774780800, 0))
	if fiveHour != "0% left" || weekly != "47% left" {
		t.Fatalf("unexpected summaries: %q %q", fiveHour, weekly)
	}
}

func TestFetch(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"email":"go@example.com","plan_type":"pro"}`))
	}))
	defer server.Close()

	cfg := config.DefaultConfig()
	cfg.Network.UsageURL = server.URL
	data, err := Fetch(server.Client(), cfg, "token")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if data["email"] != "go@example.com" {
		t.Fatalf("unexpected response: %+v", data)
	}
}
