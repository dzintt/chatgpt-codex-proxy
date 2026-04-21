package accounts

import (
	"encoding/base64"
	"encoding/json"
	"strings"
)

type tokenMetadata struct {
	Email    string
	PlanType string
	UserID   string
}

type jwtClaims struct {
	Email    string            `json:"email"`
	PlanType string            `json:"chatgpt_plan_type"`
	UserID   string            `json:"chatgpt_user_id"`
	Profile  *jwtProfileClaims `json:"https://api.openai.com/profile,omitempty"`
	Auth     *jwtAuthClaims    `json:"https://api.openai.com/auth,omitempty"`
}

type jwtProfileClaims struct {
	Email  string `json:"email"`
	UserID string `json:"chatgpt_user_id"`
}

type jwtAuthClaims struct {
	PlanType string `json:"chatgpt_plan_type"`
	UserID   string `json:"chatgpt_user_id"`
}

func metadataFromToken(token OAuthToken) tokenMetadata {
	claims, ok := parseJWTClaims(token.AccessToken)
	if !ok {
		return tokenMetadata{}
	}

	metadata := tokenMetadata{
		Email:    strings.TrimSpace(claims.Email),
		PlanType: strings.TrimSpace(claims.PlanType),
		UserID:   strings.TrimSpace(claims.UserID),
	}

	if claims.Profile != nil {
		if metadata.Email == "" {
			metadata.Email = strings.TrimSpace(claims.Profile.Email)
		}
		if metadata.UserID == "" {
			metadata.UserID = strings.TrimSpace(claims.Profile.UserID)
		}
	}

	if claims.Auth != nil {
		if metadata.PlanType == "" {
			metadata.PlanType = strings.TrimSpace(claims.Auth.PlanType)
		}
		if metadata.UserID == "" {
			metadata.UserID = strings.TrimSpace(claims.Auth.UserID)
		}
	}

	return metadata
}

func parseJWTClaims(token string) (jwtClaims, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return jwtClaims{}, false
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return jwtClaims{}, false
	}

	var claims jwtClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return jwtClaims{}, false
	}
	return claims, true
}
