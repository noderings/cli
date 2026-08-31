package auth

import (
	"time"
)

// Token represents an authentication token
type Token struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresAt    time.Time `json:"expires_at"`
	TokenType    string    `json:"token_type"`
	Scope        string    `json:"scope"`
}

// IsExpired checks if the token is expired
func (t *Token) IsExpired() bool {
	return time.Now().After(t.ExpiresAt)
}

// IsExpiringSoon checks if the token is expiring soon (within threshold)
func (t *Token) IsExpiringSoon(threshold time.Duration) bool {
	return time.Now().Add(threshold).After(t.ExpiresAt)
}
