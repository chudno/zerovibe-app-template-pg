package domain

import "time"

// PasswordReset — одноразовый токен сброса пароля с ограниченным сроком жизни.
// UsedAt непусто → токен уже использован и больше не годен.
type PasswordReset struct {
	Token     string
	UserID    int64
	CreatedAt time.Time
	ExpiresAt time.Time
	UsedAt    time.Time
}

// Usable сообщает, годен ли токен к моменту now: не использован и не истёк.
func (p PasswordReset) Usable(now time.Time) bool {
	return p.UsedAt.IsZero() && now.Before(p.ExpiresAt)
}
