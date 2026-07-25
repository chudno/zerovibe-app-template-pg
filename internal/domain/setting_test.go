package domain

import (
	"errors"
	"testing"
)

func TestValidateSetting_BoolNormalization(t *testing.T) {
	cases := map[string]string{
		"yes": "true", "on": "true", "1": "true", "TRUE": "true",
		"no": "false", "off": "false", "0": "false", "": "false",
	}
	for in, want := range cases {
		got, err := ValidateSetting("allow_signup", in)
		if err != nil {
			t.Errorf("ValidateSetting(allow_signup, %q): неожиданная ошибка %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("ValidateSetting(allow_signup, %q) = %q, ожидалось %q", in, got, want)
		}
	}
}

func TestValidateSetting_UnknownKey(t *testing.T) {
	_, err := ValidateSetting("does_not_exist", "x")
	var ve ErrValidation
	if !errors.As(err, &ve) {
		t.Fatalf("ожидалась ErrValidation, получено %v", err)
	}
}

func TestValidateSetting_BadBool(t *testing.T) {
	_, err := ValidateSetting("allow_signup", "может быть")
	var ve ErrValidation
	if !errors.As(err, &ve) {
		t.Fatalf("ожидалась ErrValidation, получено %v", err)
	}
}

// TestSettingRegistry_SecretKind проверяет, что вид настройки берётся из реестра.
// Временно добавляем секрет в реестр (тот же пакет) и восстанавливаем после.
func TestSettingRegistry_SecretKind(t *testing.T) {
	orig := settingRegistry
	settingRegistry = append([]SettingDef{}, orig...)
	settingRegistry = append(settingRegistry, SettingDef{
		Key: "test_secret", Kind: SettingSecret, Type: SettingString, Default: "", Title: "Тестовый секрет",
	})
	t.Cleanup(func() { settingRegistry = orig })

	def, ok := LookupSettingDef("test_secret")
	if !ok {
		t.Fatal("секрет не найден в реестре")
	}
	if def.Kind != SettingSecret {
		t.Errorf("ожидался вид secret, получено %q", def.Kind)
	}
	// Значение секрета валидируется как обычная строка.
	got, err := ValidateSetting("test_secret", "  s3cr3t  ")
	if err != nil {
		t.Fatalf("валидация секрета: %v", err)
	}
	if got != "s3cr3t" {
		t.Errorf("строковое значение должно тримиться, получено %q", got)
	}
}
