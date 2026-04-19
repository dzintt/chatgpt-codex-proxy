package codex

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"chatgpt-codex-proxy/internal/accounts"
	"chatgpt-codex-proxy/internal/config"
)

const (
	deviceUserCodePath = "/api/accounts/deviceauth/usercode"
	deviceTokenPath    = "/api/accounts/deviceauth/token"
	oauthTokenPath     = "/oauth/token"
	deviceAuthPath     = "/codex/device"
	deviceRedirectPath = "/deviceauth/callback"
)

type DeviceCodeResponse struct {
	UserCode     string `json:"user_code"`
	DeviceAuthID string `json:"device_auth_id"`
	Interval     int    `json:"interval"`
}

func (d *DeviceCodeResponse) UnmarshalJSON(data []byte) error {
	type rawDeviceCodeResponse struct {
		UserCode     string `json:"user_code"`
		DeviceAuthID string `json:"device_auth_id"`
		Interval     any    `json:"interval"`
	}

	var raw rawDeviceCodeResponse
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	d.UserCode = raw.UserCode
	d.DeviceAuthID = raw.DeviceAuthID
	switch value := raw.Interval.(type) {
	case float64:
		d.Interval = int(value)
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil {
			return fmt.Errorf("parse device auth interval %q: %w", value, err)
		}
		d.Interval = parsed
	case nil:
		d.Interval = 0
	default:
		return fmt.Errorf("unsupported device auth interval type %T", value)
	}
	return nil
}

type DevicePollResponse struct {
	AuthorizationCode string `json:"authorization_code"`
	CodeVerifier      string `json:"code_verifier"`
}

type OAuthService struct {
	cfg    config.Config
	client *http.Client
}

func NewOAuthService(cfg config.Config) *OAuthService {
	return &OAuthService{
		cfg: cfg,
		client: &http.Client{
			Timeout: cfg.RequestTimeout,
		},
	}
}

func (s *OAuthService) DeviceAuthURL() string {
	return strings.TrimRight(s.cfg.AuthIssuer, "/") + deviceAuthPath
}

func (s *OAuthService) RequestDeviceCode(ctx context.Context) (DeviceCodeResponse, error) {
	endpoint := strings.TrimRight(s.cfg.AuthIssuer, "/") + deviceUserCodePath
	body := map[string]string{"client_id": s.cfg.OAuthClientID}
	return doJSON[DeviceCodeResponse](ctx, s.client, http.MethodPost, endpoint, body, s.defaultHeaders())
}

func (s *OAuthService) PollDeviceCode(ctx context.Context, deviceAuthID, userCode string) (*DevicePollResponse, error) {
	endpoint := strings.TrimRight(s.cfg.AuthIssuer, "/") + deviceTokenPath
	body := map[string]string{
		"device_auth_id": deviceAuthID,
		"user_code":      userCode,
	}
	result, pending, err := doJSONAllowPending[DevicePollResponse](ctx, s.client, http.MethodPost, endpoint, body, s.defaultHeaders(), map[int]struct{}{
		http.StatusForbidden: {},
		http.StatusNotFound:  {},
	})
	if err != nil {
		return nil, err
	}
	if pending {
		return nil, nil
	}
	return &result, nil
}

func (s *OAuthService) ExchangeAuthorizationCode(ctx context.Context, authorizationCode, codeVerifier string) (accounts.OAuthToken, string, error) {
	endpoint := strings.TrimRight(s.cfg.AuthIssuer, "/") + oauthTokenPath
	values := url.Values{}
	values.Set("grant_type", "authorization_code")
	values.Set("code", authorizationCode)
	values.Set("redirect_uri", strings.TrimRight(s.cfg.AuthIssuer, "/")+deviceRedirectPath)
	values.Set("client_id", s.cfg.OAuthClientID)
	values.Set("code_verifier", codeVerifier)

	resp, err := doForm(ctx, s.client, endpoint, values, s.defaultHeaders())
	if err != nil {
		return accounts.OAuthToken{}, "", err
	}
	accountID := extractAccountID(resp)
	return buildOAuthToken(resp), accountID, nil
}

func (s *OAuthService) Refresh(ctx context.Context, existing accounts.OAuthToken, accountID string) (accounts.OAuthToken, string, error) {
	endpoint := strings.TrimRight(s.cfg.AuthIssuer, "/") + oauthTokenPath
	values := url.Values{}
	values.Set("grant_type", "refresh_token")
	values.Set("refresh_token", existing.RefreshToken)
	values.Set("client_id", s.cfg.OAuthClientID)

	resp, err := doForm(ctx, s.client, endpoint, values, s.defaultHeaders())
	if err != nil {
		return accounts.OAuthToken{}, "", err
	}
	nextAccountID := extractAccountID(resp)
	if nextAccountID == "" {
		nextAccountID = accountID
	}
	return buildOAuthToken(resp), nextAccountID, nil
}

func buildOAuthToken(raw map[string]any) accounts.OAuthToken {
	expiresIn := int64(3600)
	switch value := raw["expires_in"].(type) {
	case float64:
		expiresIn = int64(value)
	case int64:
		expiresIn = value
	}
	return accounts.OAuthToken{
		AccessToken:  stringValue(raw["access_token"]),
		RefreshToken: stringValue(raw["refresh_token"]),
		ExpiresAt:    time.Now().UTC().Add(time.Duration(expiresIn) * time.Second),
	}
}

func extractAccountID(raw map[string]any) string {
	for _, key := range []string{"id_token", "access_token"} {
		token := stringValue(raw[key])
		if token == "" {
			continue
		}
		claims := parseJWTClaims(token)
		if accountID := stringValue(claims["chatgpt_account_id"]); accountID != "" {
			return accountID
		}
		if nested, ok := claims["https://api.openai.com/auth"].(map[string]any); ok {
			if accountID := stringValue(nested["chatgpt_account_id"]); accountID != "" {
				return accountID
			}
		}
	}
	return ""
}

func parseJWTClaims(token string) map[string]any {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil
	}
	return claims
}

func (s *OAuthService) defaultHeaders() http.Header {
	headers := make(http.Header)
	headers.Set("Content-Type", "application/json")
	headers.Set("User-Agent", userAgent(s.cfg))
	return headers
}

func CodeChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func doJSON[T any](ctx context.Context, client *http.Client, method, endpoint string, body any, headers http.Header) (T, error) {
	result, _, err := doJSONAllowPending[T](ctx, client, method, endpoint, body, headers, nil)
	return result, err
}

func doJSONAllowPending[T any](ctx context.Context, client *http.Client, method, endpoint string, body any, headers http.Header, pendingStatuses map[int]struct{}) (T, bool, error) {
	var zero T
	payload, err := json.Marshal(body)
	if err != nil {
		return zero, false, err
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, strings.NewReader(string(payload)))
	if err != nil {
		return zero, false, err
	}
	req.Header = headers.Clone()
	resp, err := client.Do(req)
	if err != nil {
		return zero, false, err
	}
	defer resp.Body.Close()
	if _, ok := pendingStatuses[resp.StatusCode]; ok {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 32*1024))
		return zero, true, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 32*1024))
		return zero, false, fmt.Errorf("oauth request failed: %s", strings.TrimSpace(string(bodyBytes)))
	}
	var decoded T
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return zero, false, err
	}
	return decoded, false, nil
}

func doForm(ctx context.Context, client *http.Client, endpoint string, values url.Values, headers http.Header) (map[string]any, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(values.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header = headers.Clone()
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 32*1024))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("oauth token exchange failed: %s", strings.TrimSpace(string(body)))
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		return nil, err
	}
	return decoded, nil
}

func stringValue(value any) string {
	if str, ok := value.(string); ok {
		return str
	}
	return ""
}
