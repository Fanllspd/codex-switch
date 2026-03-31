package auth

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"codex-switch/internal/config"
)

func TestDecodeJWTPayload(t *testing.T) {
	t.Parallel()

	token := tokenWithClaims(map[string]any{"iat": 100, "exp": 200, "email": "test@example.com"})
	payload := DecodeJWTPayload(token)

	if payload["email"] != "test@example.com" {
		t.Fatalf("expected email claim, got %#v", payload["email"])
	}
	if got := ExpirationUnix(token); got == nil || *got != 200 {
		t.Fatalf("expected exp=200, got %v", got)
	}
}

func TestRefreshAuthFileIfNeeded(t *testing.T) {
	t.Parallel()

	var gotClientID string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &payload)
		gotClientID, _ = payload["client_id"].(string)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  tokenWithClaims(map[string]any{"iat": 200, "exp": 400}),
			"id_token":      tokenWithClaims(map[string]any{"iat": 200, "exp": 300, "https://api.openai.com/auth": map[string]any{"chatgpt_account_id": "acct-1"}}),
			"refresh_token": "new-refresh",
		})
	}))
	defer server.Close()

	paths := config.PathsFromHome(t.TempDir())
	if err := os.MkdirAll(filepath.Dir(paths.AuthFile), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	doc := Document{
		"tokens": map[string]any{
			"access_token":  tokenWithClaims(map[string]any{"iat": 100, "exp": 101, "client_id": "client-from-access"}),
			"id_token":      tokenWithClaims(map[string]any{"iat": 100, "exp": 101}),
			"refresh_token": "refresh-1",
		},
	}
	if err := SaveDocument(paths.AuthFile, doc); err != nil {
		t.Fatalf("SaveDocument: %v", err)
	}

	cfg := config.DefaultConfig()
	cfg.Network.RefreshURL = server.URL

	refreshed, err := RefreshAuthFileIfNeeded(server.Client(), cfg, paths.AuthFile, false, time.Unix(150, 0))
	if err != nil {
		t.Fatalf("RefreshAuthFileIfNeeded: %v", err)
	}
	if !refreshed {
		t.Fatalf("expected refresh to occur")
	}
	if gotClientID != "client-from-access" {
		t.Fatalf("expected inferred client id, got %q", gotClientID)
	}

	updated, err := LoadDocument(paths.AuthFile)
	if err != nil {
		t.Fatalf("LoadDocument: %v", err)
	}
	if info, err := os.Stat(paths.AuthFile); err != nil {
		t.Fatalf("stat auth file: %v", err)
	} else if info.Mode().Perm() != 0o600 {
		t.Fatalf("expected auth file mode 0600, got %o", info.Mode().Perm())
	}
	tokens := Tokens(updated)
	if tokens["refresh_token"] != "new-refresh" || tokens["account_id"] != "acct-1" {
		t.Fatalf("unexpected updated tokens: %+v", tokens)
	}
}

func TestResolveRefreshClientIDFallsBackToConfig(t *testing.T) {
	t.Parallel()

	cfg := config.DefaultConfig()
	cfg.Network.RefreshClientID = "config-client"

	got := ResolveRefreshClientID(map[string]string{
		"access_token": tokenWithClaims(map[string]any{"iat": 1, "exp": 2}),
		"id_token":     tokenWithClaims(map[string]any{"iat": 1, "exp": 2}),
	}, cfg)

	if got != "config-client" {
		t.Fatalf("expected config fallback, got %q", got)
	}
}

func TestAccountIDInfersFromJWTClaims(t *testing.T) {
	t.Parallel()

	tokens := map[string]string{
		"id_token": tokenWithClaims(map[string]any{
			"https://api.openai.com/auth": map[string]any{
				"chatgpt_account_id": "acct-from-jwt",
			},
		}),
	}

	if got := AccountID(tokens); got != "acct-from-jwt" {
		t.Fatalf("expected inferred account id, got %q", got)
	}
	if tokens["account_id"] != "acct-from-jwt" {
		t.Fatalf("expected inferred account id to be cached in tokens, got %+v", tokens)
	}
}

func tokenWithClaims(claims map[string]any) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	payloadBytes, _ := json.Marshal(claims)
	payload := base64.RawURLEncoding.EncodeToString(payloadBytes)
	return header + "." + payload + ".sig"
}
