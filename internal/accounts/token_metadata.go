package accounts

import (
	"encoding/base64"
	"encoding/json"
	"strings"
)

type tokenMetadata struct {
	Email    string
	PlanType string
}

func metadataFromToken(token OAuthToken) tokenMetadata {
	claims := parseJWTClaims(token.AccessToken)
	if len(claims) == 0 {
		return tokenMetadata{}
	}

	metadata := tokenMetadata{
		Email:    strings.TrimSpace(stringValue(claims["email"])),
		PlanType: strings.TrimSpace(stringValue(claims["chatgpt_plan_type"])),
	}

	if profile, ok := claims["https://api.openai.com/profile"].(map[string]any); ok {
		if metadata.Email == "" {
			metadata.Email = strings.TrimSpace(stringValue(profile["email"]))
		}
	}

	if authPayload, ok := claims["https://api.openai.com/auth"].(map[string]any); ok {
		if metadata.PlanType == "" {
			metadata.PlanType = strings.TrimSpace(stringValue(authPayload["chatgpt_plan_type"]))
		}
	}

	return metadata
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

func stringValue(value any) string {
	str, _ := value.(string)
	return str
}
