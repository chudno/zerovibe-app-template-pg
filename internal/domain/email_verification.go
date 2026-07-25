package domain

import "time"

// EmailVerification — одноразовый токен подтверждения адреса почты с TTL.
// UsedAt непусто → токен уже использован. Отдельная сущность от сброса пароля
// (механизмы независимы), хоть и устроена так же.
type EmailVerification struct {
	Token     string
	UserID    int64
	CreatedAt time.Time
	ExpiresAt time.Time
	UsedAt    time.Time
}

// Usable сообщает, годен ли токен к моменту now: не использован и не истёк.
func (v EmailVerification) Usable(now time.Time) bool {
	return v.UsedAt.IsZero() && now.Before(v.ExpiresAt)
}
