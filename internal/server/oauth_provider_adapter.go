package server

import (
	"context"

	"chatgpt-codex-proxy/internal/accounts"
	"chatgpt-codex-proxy/internal/admin"
	"chatgpt-codex-proxy/internal/codex"
)

type adminOAuthProvider struct {
	oauth *codex.OAuthService
}

func (p adminOAuthProvider) DeviceAuthURL() string {
	return p.oauth.DeviceAuthURL()
}

func (p adminOAuthProvider) RequestDeviceCode(ctx context.Context) (admin.OAuthDeviceCode, error) {
	resp, err := p.oauth.RequestDeviceCode(ctx)
	if err != nil {
		return admin.OAuthDeviceCode{}, err
	}
	return admin.OAuthDeviceCode{
		UserCode:     resp.UserCode,
		DeviceAuthID: resp.DeviceAuthID,
		Interval:     resp.Interval,
	}, nil
}

func (p adminOAuthProvider) PollDeviceCode(ctx context.Context, deviceAuthID, userCode string) (*admin.OAuthDevicePollResult, error) {
	resp, err := p.oauth.PollDeviceCode(ctx, deviceAuthID, userCode)
	if err != nil || resp == nil {
		return nil, err
	}
	return &admin.OAuthDevicePollResult{
		AuthorizationCode: resp.AuthorizationCode,
		CodeVerifier:      resp.CodeVerifier,
	}, nil
}

func (p adminOAuthProvider) ExchangeAuthorizationCode(ctx context.Context, authorizationCode, codeVerifier string) (accounts.OAuthToken, string, error) {
	return p.oauth.ExchangeAuthorizationCode(ctx, authorizationCode, codeVerifier)
}
