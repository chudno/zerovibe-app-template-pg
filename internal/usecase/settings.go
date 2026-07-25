// Настройки приложения: бизнес-логика поверх реестра доменных настроек.
// Сервис валидирует значения через domain, хранит их в репозитории и отдаёт
// типизированные значения с дефолтами из реестра. Секреты при перечислении
// маскируются — наружу уходит только признак «задано».
package usecase

import (
	"context"
	"errors"
	"time"

	"github.com/chudno/zerovibe/internal/domain"
)

// SettingRepository — порт хранилища настроек. Реализуется в repository/pgrepo.
type SettingRepository interface {
	Get(ctx context.Context, key string) (domain.Setting, error) // ErrNotFound если не задано
	Set(ctx context.Context, s domain.Setting) error
	List(ctx context.Context) ([]domain.Setting, error)
}

// SettingsService — операции над настройками приложения.
type SettingsService struct {
	repo SettingRepository
	now  func() time.Time
}

// NewSettingsService собирает сервис настроек.
func NewSettingsService(repo SettingRepository) *SettingsService {
	return &SettingsService{repo: repo, now: time.Now}
}

// Set валидирует значение по реестру и сохраняет настройку.
func (s *SettingsService) Set(ctx context.Context, key, value string) error {
	norm, err := domain.ValidateSetting(key, value)
	if err != nil {
		return err
	}
	return s.repo.Set(ctx, domain.Setting{Key: key, Value: norm, UpdatedAt: s.now().UTC()})
}

// SettingView — представление настройки для перечисления (API/UI). Для секретов
// Value пустой, а Set сообщает, задано ли значение.
type SettingView struct {
	Key   string
	Kind  domain.SettingKind
	Type  domain.SettingType
	Title string
	Value string // для config — текущее/дефолтное значение; для secret — всегда ""
	Set   bool   // задано ли значение (для секретов — единственный наблюдаемый признак)
}

// All перечисляет все известные настройки с текущими значениями. Секреты маскируются.
func (s *SettingsService) All(ctx context.Context) ([]SettingView, error) {
	stored, err := s.repo.List(ctx)
	if err != nil {
		return nil, err
	}
	byKey := make(map[string]domain.Setting, len(stored))
	for _, st := range stored {
		byKey[st.Key] = st
	}

	defs := domain.SettingDefs()
	views := make([]SettingView, 0, len(defs))
	for _, d := range defs {
		st, set := byKey[d.Key]
		v := SettingView{Key: d.Key, Kind: d.Kind, Type: d.Type, Title: d.Title, Set: set}
		switch d.Kind {
		case domain.SettingSecret:
			// значение не раскрываем
		default:
			if set {
				v.Value = st.Value
			} else {
				v.Value = d.Default
			}
		}
		views = append(views, v)
	}
	return views, nil
}

// raw возвращает сохранённое значение или дефолт из реестра, если не задано.
func (s *SettingsService) raw(ctx context.Context, key string) (string, error) {
	st, err := s.repo.Get(ctx, key)
	if err == nil {
		return st.Value, nil
	}
	var nf domain.ErrNotFound
	if errors.As(err, &nf) {
		if d, ok := domain.LookupSettingDef(key); ok {
			return d.Default, nil
		}
		return "", nil
	}
	return "", err
}

// Bool возвращает значение bool-настройки (или дефолт из реестра).
func (s *SettingsService) Bool(ctx context.Context, key string) (bool, error) {
	v, err := s.raw(ctx, key)
	if err != nil {
		return false, err
	}
	return domain.BoolValue(v), nil
}

// String возвращает значение строковой настройки (или дефолт из реестра).
func (s *SettingsService) String(ctx context.Context, key string) (string, error) {
	return s.raw(ctx, key)
}
