// Package adminres регистрирует конкретные сущности приложения в админ-реестре.
// Отделён от пакета admin (generic-ядро не знает о Note/User) — здесь стык: дескриптор
// связывает поля сущности с её репозиторием. ОБРАЗЕЦ ДЛЯ ГЕНЕРАЦИИ: новая сущность
// под админку описывается такой же функцией RegisterX(reg, repo).
package adminres

import (
	"context"
	"strconv"
	"time"

	"github.com/chudno/zerovibe/internal/admin"
	"github.com/chudno/zerovibe/internal/domain"
)

// formatDate переводит YYYY-MM-DD в DD.MM.YYYY для показа в списке. Некорректную
// строку отдаёт как есть (на всякий случай).
func formatDate(s string) string {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return s
	}
	return t.Format("02.01.2006")
}

// NoteAdminRepo — что админке нужно от репозитория заметок (admin-CRUD над всеми).
type NoteAdminRepo interface {
	ListAll(ctx context.Context) ([]domain.Note, error)
	GetByID(ctx context.Context, id int64) (domain.Note, error)
	Create(ctx context.Context, n domain.Note) (domain.Note, error)
	UpdateAny(ctx context.Context, n domain.Note) error
	DeleteAny(ctx context.Context, id int64) error
}

// ownerOptions — источник вариантов для связи «Владелец» (список пользователей).
type ownerOptions func(ctx context.Context) ([]admin.Option, error)

// RegisterNote регистрирует сущность «Заметки» в админ-реестре. Это ЭТАЛОН: показывает
// все типы участников — поля разных типов, связь (Владелец → пользователь), список с
// сортировкой/поиском, полный CRUD. Новая сущность повторяет эту структуру.
func RegisterNote(reg *admin.Registry, repo NoteAdminRepo, owners ownerOptions) {
	admin.Register(reg, admin.Resource[domain.Note]{
		Name:  "notes",
		Title: "Заметки",
		Icon:  "file-text",
		Fields: []admin.Field{
			{Name: "title", Label: "Заголовок", Type: admin.FieldText, Required: true, InList: true, Sortable: true},
			{Name: "body", Label: "Текст", Type: admin.FieldTextarea, InList: false, Help: "Содержимое заметки"},
			{
				Name: "owner_id", Label: "Владелец", Type: admin.FieldRelation, Required: true, InList: true,
				RelationOptions: func(ctx context.Context) ([]admin.Option, error) { return owners(ctx) },
			},
			// FieldDate → в форме нативный календарь (input type=date).
			{Name: "due_date", Label: "Срок", Type: admin.FieldDate, InList: true, Sortable: true, Help: "Необязательно"},
			{Name: "created_at", Label: "Создана", Type: admin.FieldDate, InList: true, Sortable: true},
		},

		// Row — как заметка выглядит строкой списка.
		Row: func(n domain.Note) admin.Record {
			due := admin.Cell{}
			if n.DueDate != "" {
				due = admin.Cell{Value: n.DueDate, Display: formatDate(n.DueDate)}
			}
			return admin.Record{
				ID: strconv.FormatInt(n.ID, 10),
				Cells: map[string]admin.Cell{
					"title":      {Value: n.Title, Display: n.Title},
					"owner_id":   {Value: strconv.FormatInt(n.OwnerID, 10), Display: "#" + strconv.FormatInt(n.OwnerID, 10)},
					"due_date":   due,
					"created_at": {Value: n.CreatedAt.Format("2006-01-02"), Display: n.CreatedAt.Format("02.01.2006")},
				},
			}
		},

		List: func(ctx context.Context) ([]domain.Note, error) { return repo.ListAll(ctx) },

		// Get — значения для формы редактирования. Дата — в формате YYYY-MM-DD (его
		// ждёт input type=date).
		Get: func(ctx context.Context, id string) (admin.FormValues, error) {
			nid, _ := strconv.ParseInt(id, 10, 64)
			n, err := repo.GetByID(ctx, nid)
			if err != nil {
				return nil, err
			}
			return admin.FormValues{
				"title":    n.Title,
				"body":     n.Body,
				"owner_id": strconv.FormatInt(n.OwnerID, 10),
				"due_date": n.DueDate,
			}, nil
		},

		// Create — валидация через доменный конструктор, затем сохранение.
		Create: func(ctx context.Context, v admin.FormValues, _ map[string]admin.FileInput) error {
			n, err := domain.NewNote(v["title"], v["body"], v["due_date"])
			if err != nil {
				return err
			}
			n.OwnerID, _ = strconv.ParseInt(v["owner_id"], 10, 64)
			_, err = repo.Create(ctx, n)
			return err
		},

		Update: func(ctx context.Context, id string, v admin.FormValues, _ map[string]admin.FileInput) error {
			n, err := domain.NewNote(v["title"], v["body"], v["due_date"])
			if err != nil {
				return err
			}
			n.ID, _ = strconv.ParseInt(id, 10, 64)
			return repo.UpdateAny(ctx, n)
		},

		Delete: func(ctx context.Context, id string) error {
			nid, _ := strconv.ParseInt(id, 10, 64)
			return repo.DeleteAny(ctx, nid)
		},
	})
}
