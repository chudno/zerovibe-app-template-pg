package domain

import (
	"errors"
	"fmt"
	"time"
)

// Доменные ошибки — sentinel-значения и типы, по которым верхние слои принимают
// решения (транспорт мапит их в HTTP-коды). Проверять через errors.Is / errors.As.
//
// ОБРАЗЕЦ: новые предсказуемые ошибки добавляются сюда, чтобы транспортный слой
// мог единообразно их обрабатывать (см. internal/transport/web/errors.go).

// ErrNotFound — запрошенная сущность не существует. → HTTP 404.
type ErrNotFound struct {
	Entity string
	ID     int64
}

func (e ErrNotFound) Error() string {
	return fmt.Sprintf("%s с id=%d не найден", e.Entity, e.ID)
}

// ErrValidation — нарушен инвариант сущности (некорректный ввод). → HTTP 400.
type ErrValidation struct {
	Field string
	Msg   string
}

func (e ErrValidation) Error() string {
	if e.Field == "" {
		return e.Msg
	}
	return fmt.Sprintf("%s: %s", e.Field, e.Msg)
}

// Ошибки аутентификации/авторизации. Sentinel-значения — проверять через errors.Is.
var (
	// ErrInvalidCredentials — неверный email или пароль. → HTTP 401.
	// Единая ошибка и для «нет такого пользователя», и для «пароль не подошёл» —
	// чтобы по ответу нельзя было определить, существует ли email.
	ErrInvalidCredentials = errors.New("неверный email или пароль")
	// ErrSignupClosed — регистрация выключена настройкой. → HTTP 403.
	ErrSignupClosed = errors.New("регистрация закрыта")
	// ErrEmailTaken — email уже зарегистрирован. → HTTP 409.
	ErrEmailTaken = errors.New("этот email уже зарегистрирован")
	// ErrUnauthenticated — нет валидной сессии. → редирект на вход (или 401 для htmx).
	ErrUnauthenticated = errors.New("требуется вход")
	// ErrForbidden — недостаточно прав (роль). → HTTP 403.
	ErrForbidden = errors.New("недостаточно прав")
	// ErrInvalidToken — токен сброса не найден/просрочен/использован. → HTTP 400.
	ErrInvalidToken = errors.New("ссылка недействительна или устарела")
	// ErrEmailNotVerified — почта не подтверждена, а это требуется настройкой.
	// → вход блокируется, показываем «подтвердите почту».
	ErrEmailNotVerified = errors.New("подтвердите адрес почты по ссылке из письма")
	// ErrSetupClosed — первичная настройка уже завершена (админ создан). → HTTP 410.
	ErrSetupClosed = errors.New("первичная настройка уже завершена")
	// ErrSetupToken — неверный setup-токен. → HTTP 403.
	ErrSetupToken = errors.New("неверный код настройки")
)

// ErrRateLimited — превышен лимит попыток. → HTTP 429. Тип (не sentinel), чтобы нести
// RetryAfter для заголовка Retry-After.
type ErrRateLimited struct {
	RetryAfter time.Duration
}

func (e ErrRateLimited) Error() string {
	return "слишком много попыток, попробуйте позже"
}
