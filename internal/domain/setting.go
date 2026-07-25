// Настройки приложения — типизированный реестр известных ключей. Значения хранятся
// строками, но каждый ключ имеет тип и вид (обычная настройка/секрет). Реестр —
// единственное место, где объявляются доступные настройки: задать неизвестный ключ
// нельзя. Это защищает от опечаток и мусора, и даёт агенту/UI понятный список.
package domain

import (
	"strconv"
	"strings"
	"time"
)

// SettingKind — вид настройки.
type SettingKind string

const (
	// SettingConfig — обычная настройка: значение читается обратно (в API/UI).
	SettingConfig SettingKind = "config"
	// SettingSecret — секрет: значение принимается и используется, но наружу не
	// отдаётся (в ответах — только признак «задано», в логах маскируется).
	SettingSecret SettingKind = "secret"
)

// SettingType — тип значения настройки (для валидации ввода).
type SettingType string

const (
	SettingBool   SettingType = "bool"
	SettingString SettingType = "string"
)

// SettingDef — описание известной настройки в реестре.
type SettingDef struct {
	Key     string
	Kind    SettingKind
	Type    SettingType
	Default string // строковое представление значения по умолчанию
	Title   string // человекочитаемое название (для будущего UI)
}

// settingRegistry — реестр всех доступных настроек приложения. Новая настройка
// добавляется сюда (и больше нигде) — после этого её можно задавать через API.
var settingRegistry = []SettingDef{
	{Key: "allow_signup", Kind: SettingConfig, Type: SettingBool, Default: "true", Title: "Открытая регистрация"},
	{Key: "require_email_verification", Kind: SettingConfig, Type: SettingBool, Default: "false", Title: "Требовать подтверждение почты"},
	{Key: "app_name", Kind: SettingConfig, Type: SettingString, Default: "Zerovibe", Title: "Название приложения"},
}

// SettingDefs возвращает копию реестра (для перечисления в API/UI).
func SettingDefs() []SettingDef {
	out := make([]SettingDef, len(settingRegistry))
	copy(out, settingRegistry)
	return out
}

// LookupSettingDef ищет описание настройки по ключу.
func LookupSettingDef(key string) (SettingDef, bool) {
	for _, d := range settingRegistry {
		if d.Key == key {
			return d, true
		}
	}
	return SettingDef{}, false
}

// Setting — хранимое значение настройки.
type Setting struct {
	Key       string
	Value     string
	UpdatedAt time.Time
}

// ValidateSetting проверяет, что ключ известен реестру и значение подходит под тип.
// Возвращает нормализованное значение (например, "true"/"false" для bool) и ошибку
// ErrValidation при нарушении. Хранение/время — забота вызывающего слоя.
func ValidateSetting(key, value string) (string, error) {
	def, ok := LookupSettingDef(key)
	if !ok {
		return "", ErrValidation{Field: "key", Msg: "неизвестная настройка"}
	}
	switch def.Type {
	case SettingBool:
		v := strings.ToLower(strings.TrimSpace(value))
		switch v {
		case "true", "1", "yes", "on":
			return "true", nil
		case "false", "0", "no", "off", "":
			return "false", nil
		default:
			return "", ErrValidation{Field: key, Msg: "ожидается да/нет"}
		}
	case SettingString:
		v := strings.TrimSpace(value)
		if len(v) > 1000 {
			return "", ErrValidation{Field: key, Msg: "значение длиннее 1000 символов"}
		}
		return v, nil
	default:
		return "", ErrValidation{Field: key, Msg: "неподдерживаемый тип настройки"}
	}
}

// BoolValue разбирает строковое значение настройки как bool (для чтения в коде).
func BoolValue(value string) bool {
	b, _ := strconv.ParseBool(strings.TrimSpace(value))
	return b
}
