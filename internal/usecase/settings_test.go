// Unit-тесты сервиса настроек на фейковом репозитории.
package usecase

import (
	"context"
	"errors"
	"testing"

	"github.com/chudno/zerovibe/internal/domain"
)

// fakeSettingRepo — in-memory реализация SettingRepository.
type fakeSettingRepo struct {
	items map[string]domain.Setting
}

func newFakeSettingRepo() *fakeSettingRepo {
	return &fakeSettingRepo{items: map[string]domain.Setting{}}
}

func (f *fakeSettingRepo) Get(_ context.Context, key string) (domain.Setting, error) {
	st, ok := f.items[key]
	if !ok {
		return domain.Setting{}, domain.ErrNotFound{Entity: "setting"}
	}
	return st, nil
}

func (f *fakeSettingRepo) Set(_ context.Context, s domain.Setting) error {
	f.items[s.Key] = s
	return nil
}

func (f *fakeSettingRepo) List(_ context.Context) ([]domain.Setting, error) {
	out := make([]domain.Setting, 0, len(f.items))
	for _, v := range f.items {
		out = append(out, v)
	}
	return out, nil
}

func TestSettings_SetConfig_OK(t *testing.T) {
	svc := NewSettingsService(newFakeSettingRepo())
	if err := svc.Set(context.Background(), "allow_signup", "yes"); err != nil {
		t.Fatalf("неожиданная ошибка: %v", err)
	}
	got, err := svc.Bool(context.Background(), "allow_signup")
	if err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Error("allow_signup должен стать true после установки 'yes'")
	}
}

func TestSettings_UnknownKey_Validation(t *testing.T) {
	svc := NewSettingsService(newFakeSettingRepo())
	err := svc.Set(context.Background(), "nope_unknown", "x")
	var ve domain.ErrValidation
	if !errors.As(err, &ve) {
		t.Fatalf("ожидалась ErrValidation для неизвестного ключа, получено %v", err)
	}
}

func TestSettings_WrongBool_Validation(t *testing.T) {
	svc := NewSettingsService(newFakeSettingRepo())
	err := svc.Set(context.Background(), "allow_signup", "абракадабра")
	var ve domain.ErrValidation
	if !errors.As(err, &ve) {
		t.Fatalf("ожидалась ErrValidation для неверного bool, получено %v", err)
	}
}

func TestSettings_DefaultsWhenUnset(t *testing.T) {
	svc := NewSettingsService(newFakeSettingRepo())
	// allow_signup по умолчанию true (эталон: регистрация открыта из коробки, чтобы
	// демо работало сразу; вайбкодер закрывает её в настройках при необходимости).
	b, err := svc.Bool(context.Background(), "allow_signup")
	if err != nil {
		t.Fatal(err)
	}
	if !b {
		t.Error("allow_signup по умолчанию должен быть true")
	}
	// app_name по умолчанию "Zerovibe" (плейсхолдер эталона; агент/вайбкодер меняет).
	s, err := svc.String(context.Background(), "app_name")
	if err != nil {
		t.Fatal(err)
	}
	if s != "Zerovibe" {
		t.Errorf("app_name по умолчанию должен быть %q, получено %q", "Zerovibe", s)
	}
}

func TestSettings_All_ConfigVisible(t *testing.T) {
	svc := NewSettingsService(newFakeSettingRepo())
	if err := svc.Set(context.Background(), "app_name", "Моё приложение"); err != nil {
		t.Fatalf("установка: %v", err)
	}
	views, err := svc.All(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, v := range views {
		if v.Key == "app_name" {
			found = true
			if v.Value != "Моё приложение" {
				t.Errorf("config-настройка должна отдавать значение, получено %q", v.Value)
			}
			if !v.Set {
				t.Error("заданная настройка должна помечаться Set=true")
			}
		}
	}
	if !found {
		t.Fatal("app_name не найдена в перечислении настроек")
	}
}
