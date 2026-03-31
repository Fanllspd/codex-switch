package auth

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"codex-switch/internal/config"
)

type Document map[string]any

type RefreshResponse struct {
	AccessToken  string `json:"access_token"`
	IDToken      string `json:"id_token"`
	RefreshToken string `json:"refresh_token"`
}

func LoadDocument(path string) (Document, error) {
	bytes, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	doc := Document{}
	if err := json.Unmarshal(bytes, &doc); err != nil {
		return nil, err
	}
	return doc, nil
}

func SaveDocument(path string, doc Document) error {
	bytes, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	bytes = append(bytes, '\n')
	return os.WriteFile(path, bytes, 0o600)
}

func Tokens(doc Document) map[string]string {
	raw, ok := doc["tokens"].(map[string]any)
	if !ok {
		return map[string]string{}
	}

	result := make(map[string]string, len(raw))
	for key, value := range raw {
		text, ok := value.(string)
		if ok {
			result[key] = text
		}
	}
	return result
}

func SetTokens(doc Document, tokens map[string]string) {
	raw := map[string]any{}
	for key, value := range tokens {
		raw[key] = value
	}
	doc["tokens"] = raw
}

func AccountID(tokens map[string]string) string {
	EnsureAccountID(tokens)
	return tokens["account_id"]
}

func DecodeJWTPayload(token string) map[string]any {
	if strings.Count(token, ".") < 2 {
		return map[string]any{}
	}

	parts := strings.Split(token, ".")
	payload := parts[1]
	if mod := len(payload) % 4; mod != 0 {
		payload += strings.Repeat("=", 4-mod)
	}

	decoded, err := base64.URLEncoding.DecodeString(payload)
	if err != nil {
		return map[string]any{}
	}

	result := map[string]any{}
	if err := json.Unmarshal(decoded, &result); err != nil {
		return map[string]any{}
	}
	return result
}

func ExpirationUnix(token string) *int64 {
	return unixField(DecodeJWTPayload(token), "exp")
}

func IssuedAtUnix(token string) *int64 {
	return unixField(DecodeJWTPayload(token), "iat")
}

func ExtractEmailAndPlan(tokens map[string]string) (string, string) {
	idPayload := DecodeJWTPayload(tokens["id_token"])
	accessPayload := DecodeJWTPayload(tokens["access_token"])

	authClaims := nestedMap(idPayload, "https://api.openai.com/auth")
	if len(authClaims) == 0 {
		authClaims = nestedMap(accessPayload, "https://api.openai.com/auth")
	}

	profileClaims := nestedMap(idPayload, "https://api.openai.com/profile")
	if len(profileClaims) == 0 {
		profileClaims = nestedMap(accessPayload, "https://api.openai.com/profile")
	}

	email := stringField(profileClaims, "email")
	if email == "" {
		email = stringField(idPayload, "email")
	}
	if email == "" {
		email = "-"
	}

	plan := stringField(authClaims, "chatgpt_plan_type")
	if plan == "" {
		plan = "-"
	}

	return email, plan
}

func EnsureAccountID(tokens map[string]string) {
	if tokens["account_id"] != "" {
		return
	}

	idPayload := DecodeJWTPayload(tokens["id_token"])
	authClaims := nestedMap(idPayload, "https://api.openai.com/auth")
	if accountID := stringField(authClaims, "chatgpt_account_id"); accountID != "" {
		tokens["account_id"] = accountID
		return
	}

	accessPayload := DecodeJWTPayload(tokens["access_token"])
	authClaims = nestedMap(accessPayload, "https://api.openai.com/auth")
	if accountID := stringField(authClaims, "chatgpt_account_id"); accountID != "" {
		tokens["account_id"] = accountID
	}
}

func InferRefreshClientID(tokens map[string]string) string {
	accessPayload := DecodeJWTPayload(tokens["access_token"])
	if clientID := stringField(accessPayload, "client_id"); clientID != "" {
		return clientID
	}

	idPayload := DecodeJWTPayload(tokens["id_token"])
	if clientID := stringField(idPayload, "client_id"); clientID != "" {
		return clientID
	}

	return ""
}

func ShouldRefreshAccessToken(accessToken string, margin time.Duration, now time.Time) bool {
	expiration := ExpirationUnix(accessToken)
	if expiration == nil {
		return true
	}

	return time.Unix(*expiration, 0).Before(now.Add(margin)) || time.Unix(*expiration, 0).Equal(now.Add(margin))
}

func ResolveRefreshClientID(tokens map[string]string, cfg config.Config) string {
	if inferred := InferRefreshClientID(tokens); inferred != "" {
		return inferred
	}
	return strings.TrimSpace(cfg.Network.RefreshClientID)
}

func RefreshWithToken(client *http.Client, cfg config.Config, refreshToken string, clientID string) (*RefreshResponse, error) {
	if strings.TrimSpace(refreshToken) == "" {
		return nil, fmt.Errorf("missing refresh_token")
	}
	if strings.TrimSpace(clientID) == "" {
		return nil, fmt.Errorf("missing refresh client id")
	}

	payload, err := json.Marshal(map[string]any{
		"client_id":     clientID,
		"grant_type":    "refresh_token",
		"refresh_token": refreshToken,
	})
	if err != nil {
		return nil, err
	}

	requestCtx, cancel := context.WithTimeout(context.Background(), cfg.RefreshTimeoutDuration())
	defer cancel()

	req, err := http.NewRequestWithContext(requestCtx, http.MethodPost, cfg.Network.RefreshURL, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
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

	result := &RefreshResponse{}
	if err := json.Unmarshal(body, result); err != nil {
		return nil, fmt.Errorf("invalid json response: %w", err)
	}

	return result, nil
}

func RefreshAuthFileIfNeeded(client *http.Client, cfg config.Config, path string, force bool, now time.Time) (bool, error) {
	doc, err := LoadDocument(path)
	if err != nil {
		return false, err
	}

	tokens := Tokens(doc)
	if !force && !ShouldRefreshAccessToken(tokens["access_token"], cfg.RefreshMarginDuration(), now) {
		return false, nil
	}

	refreshed, err := RefreshWithToken(client, cfg, tokens["refresh_token"], ResolveRefreshClientID(tokens, cfg))
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(refreshed.AccessToken) == "" {
		return false, fmt.Errorf("refresh returned no access_token")
	}

	tokens["access_token"] = refreshed.AccessToken
	if refreshed.IDToken != "" {
		tokens["id_token"] = refreshed.IDToken
	}
	if refreshed.RefreshToken != "" {
		tokens["refresh_token"] = refreshed.RefreshToken
	}
	EnsureAccountID(tokens)

	SetTokens(doc, tokens)
	doc["last_refresh"] = now.UTC().Format(time.RFC3339Nano)
	if err := SaveDocument(path, doc); err != nil {
		return false, err
	}

	return true, nil
}

func unixField(payload map[string]any, key string) *int64 {
	switch value := payload[key].(type) {
	case float64:
		parsed := int64(value)
		return &parsed
	case int64:
		parsed := value
		return &parsed
	default:
		return nil
	}
}

func nestedMap(payload map[string]any, key string) map[string]any {
	value, ok := payload[key].(map[string]any)
	if !ok {
		return map[string]any{}
	}
	return value
}

func stringField(payload map[string]any, key string) string {
	value, ok := payload[key].(string)
	if !ok {
		return ""
	}
	return value
}
