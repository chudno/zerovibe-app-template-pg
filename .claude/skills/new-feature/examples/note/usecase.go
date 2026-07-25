// Package usecase содержит бизнес-логику (оркестрацию) и порты — интерфейсы
// репозиториев. Зависит ТОЛЬКО от domain. Конкретные реализации хранилища
// (repository/pgrepo) внедряются снаружи и реализуют эти интерфейсы — инверсия зависимостей.
//
// ОБРАЗЕЦ ДЛЯ ГЕНЕРАЦИИ: на каждую сущность — порт (NoteRepository) + сервис
// (NoteService) с методами-операциями. Сервис валидирует через domain-конструкторы
// и делегирует хранение порту. Здесь нет ни SQL, ни HTTP.
package usecase

import (
	"context"

	"github.com/chudno/zerovibe/internal/domain"
)

// NoteRepository — порт хранилища заметок. Реализуется в repository/ydb.
// Заметки личные: операции привязаны к владельцу (ownerID).
type NoteRepository interface {
	Create(ctx context.Context, n domain.Note) (domain.Note, error) // n.OwnerID уже задан
	ListByOwner(ctx context.Context, ownerID int64) ([]domain.Note, error)
	Delete(ctx context.Context, id, ownerID int64) error // удаляет только свою; иначе ErrNotFound
}

// NoteService — бизнес-операции над заметками.
type NoteService struct {
	repo NoteRepository
}

// NewNoteService собирает сервис с внедрённым репозиторием.
func NewNoteService(repo NoteRepository) *NoteService {
	return &NoteService{repo: repo}
}

// Create валидирует ввод через доменный конструктор и сохраняет заметку владельца.
func (s *NoteService) Create(ctx context.Context, ownerID int64, title, body string) (domain.Note, error) {
	n, err := domain.NewNote(title, body, "")
	if err != nil {
		return domain.Note{}, err
	}
	n.OwnerID = ownerID
	return s.repo.Create(ctx, n)
}

// List возвращает заметки владельца (новые сверху — порядок задаёт репозиторий).
func (s *NoteService) List(ctx context.Context, ownerID int64) ([]domain.Note, error) {
	return s.repo.ListByOwner(ctx, ownerID)
}

// Delete удаляет заметку владельца по id (чужую не трогает — ErrNotFound).
func (s *NoteService) Delete(ctx context.Context, id, ownerID int64) error {
	return s.repo.Delete(ctx, id, ownerID)
}
