package domain

import "time"

// Session — серверная сессия пользователя. Хранится в БД (можно отозвать: логаут,
// бан, смена пароля), токен живёт в httpOnly-cookie.
type Session struct {
	Token     string
	UserID    int64
	CreatedAt time.Time
	ExpiresAt time.Time
}

// Expired сообщает, истекла ли сессия к моменту now.
func (s Session) Expired(now time.Time) bool {
	return !now.Before(s.ExpiresAt)
}
