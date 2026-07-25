// Пользователь приложения, его роль и инварианты. Слой domain — только stdlib:
// хеширование пароля живёт в usecase (это деталь алгоритма аутентификации), здесь
// валидируется лишь ПЛЕЙН-пароль (границы) и формат email/роли.
package domain

import (
	"strings"
	"time"
)

// Role — роль пользователя. Расширяемая: новая роль добавляется константой и в Valid().
// Состояния доступа в приложении: гость (нет пользователя) → user → admin.
type Role string

const (
	RoleUser  Role = "user"
	RoleAdmin Role = "admin"
)

// Valid сообщает, известна ли роль. Точка расширения при добавлении новых ролей.
func (r Role) Valid() bool {
	switch r {
	case RoleUser, RoleAdmin:
		return true
	default:
		return false
	}
}

// User — учётная запись приложения. PasswordHash проставляет usecase (bcrypt),
// domain его не вычисляет. Без json/db-тегов — представления в своих слоях.
type User struct {
	ID              int64
	Email           string
	PasswordHash    string
	Role            Role
	EmailVerifiedAt time.Time // непусто → почта подтверждена
	CreatedAt       time.Time
}

// EmailVerified сообщает, подтверждена ли почта пользователя.
func (u User) EmailVerified() bool { return !u.EmailVerifiedAt.IsZero() }

// Пароль: минимум — в рунах (дружелюбно к кириллице), максимум — в БАЙТАХ, т.к.
// bcrypt молча игнорирует всё после 72 байт (иначе длинный пароль обрезался бы
// незаметно — это дыра). Поэтому верхнюю границу считаем в байтах.
const (
	minPasswordRunes = 8
	maxPasswordBytes = 72
)

// NewUser — конструктор-валидатор. Нормализует email, проверяет формат, ПЛЕЙН-пароль
// и роль. Хеш и ID/CreatedAt проставляются на следующих слоях (usecase/репозиторий).
func NewUser(email, passwordPlain string, role Role) (User, error) {
	email = NormalizeEmail(email)
	if email == "" {
		return User{}, ErrValidation{Field: "email", Msg: "email обязателен"}
	}
	if !looksLikeEmail(email) {
		return User{}, ErrValidation{Field: "email", Msg: "похоже на некорректный email"}
	}
	if len(email) > 254 {
		return User{}, ErrValidation{Field: "email", Msg: "email слишком длинный"}
	}
	if err := ValidatePasswordPlain(passwordPlain); err != nil {
		return User{}, err
	}
	if !role.Valid() {
		return User{}, ErrValidation{Field: "role", Msg: "недопустимая роль"}
	}
	return User{Email: email, Role: role}, nil
}

// ValidatePasswordPlain проверяет ПЛЕЙН-пароль (длину). Используется и в NewUser, и
// при смене пароля (сброс), где email/роль не трогаются.
func ValidatePasswordPlain(p string) error {
	if len([]rune(p)) < minPasswordRunes {
		return ErrValidation{Field: "password", Msg: "пароль короче 8 символов"}
	}
	if len(p) > maxPasswordBytes {
		return ErrValidation{Field: "password", Msg: "пароль слишком длинный"}
	}
	return nil
}

// NormalizeEmail приводит email к каноничному виду (trim + нижний регистр). Это
// продуктовое упрощение: считаем email кейс-инсенситивным целиком.
func NormalizeEmail(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// looksLikeEmail — дешёвая проверка формата без regexp: ровно один '@', непустые
// части до/после, точка в доменной части. Достаточно для отсева опечаток.
func looksLikeEmail(s string) bool {
	at := strings.IndexByte(s, '@')
	if at <= 0 || at != strings.LastIndexByte(s, '@') {
		return false
	}
	local, domain := s[:at], s[at+1:]
	if local == "" || domain == "" {
		return false
	}
	dot := strings.IndexByte(domain, '.')
	return dot > 0 && dot < len(domain)-1
}
