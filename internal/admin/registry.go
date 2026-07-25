package admin

import (
	"context"
	"sort"
)

// ResourceHandle — дескриптор сущности БЕЗ параметра типа. Generic-хендлеры работают
// только через него: так разнотипные Resource[Note]/Resource[User] лежат в одном
// реестре. Адаптер handle[T] стирает тип T, оборачивая Resource[T].
type ResourceHandle interface {
	Name() string
	Title() string
	Icon() string
	Fields() []Field

	List(ctx context.Context) ([]Record, error)
	Get(ctx context.Context, id string) (FormValues, error)
	Create(ctx context.Context, v FormValues, files map[string]FileInput) error
	Update(ctx context.Context, id string, v FormValues, files map[string]FileInput) error
	Delete(ctx context.Context, id string) error
}

// handle[T] адаптирует типизированный Resource[T] к нетипизированному ResourceHandle.
type handle[T any] struct{ r Resource[T] }

func (h handle[T]) Name() string    { return h.r.Name }
func (h handle[T]) Title() string   { return h.r.Title }
func (h handle[T]) Fields() []Field { return h.r.Fields }

// Icon возвращает имя иконки раздела, по умолчанию "list".
func (h handle[T]) Icon() string {
	if h.r.Icon == "" {
		return "list"
	}
	return h.r.Icon
}

func (h handle[T]) List(ctx context.Context) ([]Record, error) {
	items, err := h.r.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Record, 0, len(items))
	for _, it := range items {
		out = append(out, h.r.Row(it))
	}
	return out, nil
}

func (h handle[T]) Get(ctx context.Context, id string) (FormValues, error) {
	return h.r.Get(ctx, id)
}
func (h handle[T]) Create(ctx context.Context, v FormValues, files map[string]FileInput) error {
	return h.r.Create(ctx, v, files)
}
func (h handle[T]) Update(ctx context.Context, id string, v FormValues, files map[string]FileInput) error {
	return h.r.Update(ctx, id, v, files)
}
func (h handle[T]) Delete(ctx context.Context, id string) error {
	return h.r.Delete(ctx, id)
}

// Registry — упорядоченный набор зарегистрированных сущностей. Не глобальный: его
// создаёт composition root (main.go) и передаёт в админ-сервер. Это явная зависимость,
// тестируемая и без скрытого состояния пакета.
type Registry struct {
	order  []string                  // имена в порядке регистрации (порядок в меню)
	byName map[string]ResourceHandle // быстрый доступ по имени раздела
}

// NewRegistry создаёт пустой реестр.
func NewRegistry() *Registry {
	return &Registry{byName: map[string]ResourceHandle{}}
}

// Register добавляет сущность в реестр. Дженерик-функция (не метод: методы Go не
// могут иметь своих type-параметров). Вызывается в main.go на каждую сущность.
func Register[T any](reg *Registry, r Resource[T]) {
	if _, ok := reg.byName[r.Name]; !ok {
		reg.order = append(reg.order, r.Name)
	}
	reg.byName[r.Name] = handle[T]{r: r}
}

// Get возвращает дескриптор по имени раздела (и флаг существования).
func (reg *Registry) Get(name string) (ResourceHandle, bool) {
	h, ok := reg.byName[name]
	return h, ok
}

// All возвращает дескрипторы в порядке регистрации (для меню сайдбара).
func (reg *Registry) All() []ResourceHandle {
	out := make([]ResourceHandle, 0, len(reg.order))
	for _, name := range reg.order {
		out = append(out, reg.byName[name])
	}
	return out
}

// Empty сообщает, что в реестре нет ни одной сущности (тогда админка скрыта).
func (reg *Registry) Empty() bool { return len(reg.order) == 0 }

// fieldByName ищет поле по имени в дескрипторе (помощник для рендера/сортировки).
func fieldByName(h ResourceHandle, name string) (Field, bool) {
	for _, f := range h.Fields() {
		if f.Name == name {
			return f, true
		}
	}
	return Field{}, false
}

// sortRecords сортирует записи по значению ячейки поля sortField. dir: "asc"/"desc".
// Числовые поля сравниваются как числа, остальные — лексикографически.
func sortRecords(records []Record, f Field, dir string) {
	sort.SliceStable(records, func(i, j int) bool {
		a := records[i].Cells[f.Name].Value
		b := records[j].Cells[f.Name].Value
		less := compareCells(a, b, f.Type)
		if dir == "desc" {
			return !less
		}
		return less
	})
}
