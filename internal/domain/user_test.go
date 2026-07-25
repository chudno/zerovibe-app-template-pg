package domain

import (
	"errors"
	"strings"
	"testing"
)

func TestNewUser_Roles(t *testing.T) {
	if _, err := NewUser("a@b.com", "password123", RoleUser); err != nil {
		t.Errorf("RoleUser должна быть валидна: %v", err)
	}
	if _, err := NewUser("a@b.com", "password123", RoleAdmin); err != nil {
		t.Errorf("RoleAdmin должна быть валидна: %v", err)
	}
	if _, err := NewUser("a@b.com", "password123", Role("superuser")); err == nil {
		t.Error("неизвестная роль должна отвергаться")
	}
}

func TestNewUser_NormalizesEmail(t *testing.T) {
	u, err := NewUser("  USER@Example.COM ", "password123", RoleUser)
	if err != nil {
		t.Fatal(err)
	}
	if u.Email != "user@example.com" {
		t.Errorf("email должен нормализоваться, получено %q", u.Email)
	}
}

func TestNewUser_BadEmail(t *testing.T) {
	// Валидатор намеренно дешёвый (без regexp): ловит отсутствие @, пустые части,
	// домен без точки. Тонкие случаи (пробел внутри, два @) — вне его зоны, это ок
	// для приложения такого класса; почта всё равно подтверждается письмом.
	for _, bad := range []string{"", "no-at", "a@b", "@b.com", "a@", "a@@b.com"} {
		if _, err := NewUser(bad, "password123", RoleUser); err == nil {
			t.Errorf("email %q должен быть отвергнут", bad)
		}
	}
}

func TestValidatePasswordPlain_Bounds(t *testing.T) {
	// ровно 8 рун — ок (нижняя граница включительно)
	if err := ValidatePasswordPlain("12345678"); err != nil {
		t.Errorf("8 символов должны проходить: %v", err)
	}
	// 7 рун — мало
	if err := ValidatePasswordPlain("1234567"); !isValidation(err) {
		t.Errorf("7 символов должны отвергаться, получено %v", err)
	}
	// кириллица: 8 рун (16 байт) — должно проходить (считаем минимум в рунах)
	if err := ValidatePasswordPlain("пароль12"); err != nil {
		t.Errorf("8 кириллических символов должны проходить: %v", err)
	}
	// ровно 72 байта — ок (верхняя граница включительно)
	if err := ValidatePasswordPlain(strings.Repeat("a", 72)); err != nil {
		t.Errorf("72 байта должны проходить: %v", err)
	}
	// 73 байта — много
	if err := ValidatePasswordPlain(strings.Repeat("a", 73)); !isValidation(err) {
		t.Errorf("73 байта должны отвергаться, получено %v", err)
	}
}

func isValidation(err error) bool {
	var ve ErrValidation
	return errors.As(err, &ve)
}
