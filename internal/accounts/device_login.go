package accounts

import "time"

type DeviceLoginStatus string

const (
	DeviceLoginPending DeviceLoginStatus = "pending"
	DeviceLoginReady   DeviceLoginStatus = "ready"
	DeviceLoginExpired DeviceLoginStatus = "expired"
	DeviceLoginError   DeviceLoginStatus = "error"
)

type DeviceLoginRecord struct {
	LoginID   string            `json:"login_id"`
	AuthURL   string            `json:"auth_url"`
	UserCode  string            `json:"user_code"`
	Status    DeviceLoginStatus `json:"status"`
	Error     string            `json:"error,omitempty"`
	CreatedAt time.Time         `json:"created_at"`
	ExpiresAt time.Time         `json:"expires_at"`
}
