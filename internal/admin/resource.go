// Package admin — встроенная админ-панель приложения («бэкофис из коробки»).
//
// Идея: ОДИН generic-слой (хендлеры, рендеринг, CSV-экспорт, загрузка файлов)
// обслуживает ЛЮБУЮ сущность. Сущность описывается ДЕСКРИПТОРОМ (Resource) —
// явным набором полей и функций-аксессоров, без рефлексии (в духе zerovibe:
// простой явный код). Это «Django admin без магии reflect».
//
// Чтобы сущность появилась в админке, достаточно зарегистрировать её дескриптор:
//
//	admin.Register(admin.Resource[domain.Note]{
//	    Name: "notes", Title: "Заметки",
//	    Fields: []admin.Field{ ... },
//	    List:   func(ctx) ([]domain.Note, error) { ... },
//	    ...
//	})
//
// Generic-слой не знает о конкретных типах — он работает через RecordSet, который
// дескриптор отдаёт. Типобезопасность обеспечивают замыкания внутри Resource.
package admin

import (
	"context"
	"io"
)

// FieldType — тип поля в форме и в списке. Определяет, как поле редактируется
// (контрол формы) и как отображается в таблице.
type FieldType string

const (
	FieldText     FieldType = "text"     // короткая строка
	FieldTextarea FieldType = "textarea" // многострочный текст
	FieldNumber   FieldType = "number"   // целое/дробное число
	FieldDate     FieldType = "date"     // дата (YYYY-MM-DD)
	FieldBool     FieldType = "bool"     // да/нет (switch в форме, галочка в списке)
	FieldSelect   FieldType = "select"   // выбор из фиксированного списка Options
	FieldRelation FieldType = "relation" // связь с другой сущностью (combobox по имени)
	FieldFile     FieldType = "file"     // загрузка файла (ключ хранится в записи)
	FieldBadge    FieldType = "badge"    // статус-пилюля в списке (значение из Options)
)

// Option — вариант для select/relation/badge: машинное значение и человекочитаемая
// подпись. Для FieldBadge поле Tone задаёт цвет пилюли (см. шаблон).
type Option struct {
	Value string
	Label string
	Tone  string // "", "success", "warning", "destructive", "info" — только для badge
}

// Field — описание одного поля сущности: как его звать, как редактировать и
// показывать. Это чистые ДАННЫЕ (без логики доступа к самой сущности — та в Resource).
type Field struct {
	Name     string    // машинное имя (ключ в форме, заголовок CSV-колонки)
	Label    string    // человекочитаемая подпись
	Type     FieldType // тип контрола/отображения
	Required bool      // обязательное при создании/редактировании
	InList   bool      // показывать колонкой в списке
	Sortable bool      // заголовок колонки кликабелен для сортировки
	Help     string    // подпись-описание под полем в форме

	// Options — варианты для select/badge. Для relation варианты подгружаются
	// динамически (RelationOptions), здесь могут быть пусты.
	Options []Option

	// RelationOptions — для FieldRelation: подгрузка вариантов связи (напр. список
	// пользователей для поля «Владелец»). nil для прочих типов.
	RelationOptions func(ctx context.Context) ([]Option, error)
}

// Cell — отрендеренное значение поля для строки списка: машинное значение (для
// сортировки/CSV) и то, как его показать (Display) с учётом типа (badge tone и т.п.).
type Cell struct {
	Value   string // сырое значение (CSV, сортировка)
	Display string // как показать в таблице (для relation — имя, для file — имя файла)
	Tone    string // для badge — цвет пилюли
	IsFile  bool   // file-ячейка → показать миниатюру/иконку
}

// Record — одна запись сущности в обобщённом виде: идентификатор и ячейки по полям.
type Record struct {
	ID    string
	Cells map[string]Cell // ключ — Field.Name
}

// FormValues — значения формы при создании/редактировании (ключ — Field.Name).
// Для file-полей значение — это io.Reader загруженного файла (см. FileInput).
type FormValues map[string]string

// FileInput — загруженный через форму файл (имя + содержимое + размер).
type FileInput struct {
	FileName string
	Content  io.Reader
	Size     int64
}

// Resource — дескриптор сущности для админки. Параметризован типом сущности T,
// но generic-слой работает с ним через интерфейс ResourceHandle (стирание типа).
//
// Функции-аксессоры (List/Get/Create/Update/Delete/Row) — единственное место, где
// дескриптор знает о конкретном типе T. Их пишет автор сущности (или скилл
// new-feature по эталону). Всё остальное (рендер, формы, CSV, файлы) — общее.
type Resource[T any] struct {
	Name   string  // машинное имя раздела (URL: /admin/<name>), напр. "notes"
	Title  string  // заголовок раздела в меню/шапке, напр. "Заметки"
	Icon   string  // имя Lucide-иконки раздела для сайдбара (пусто → дефолт "list")
	Fields []Field // поля сущности

	// Row превращает сущность в строку списка (ячейки по полям).
	Row func(t T) Record
	// List возвращает все записи (generic-слой сам отфильтрует/отсортирует/постранично).
	List func(ctx context.Context) ([]T, error)
	// Get возвращает одну запись по id (для формы редактирования). Заполняет FormValues.
	Get func(ctx context.Context, id string) (FormValues, error)
	// Create создаёт запись из значений формы и файлов. Возвращает ошибку валидации.
	Create func(ctx context.Context, v FormValues, files map[string]FileInput) error
	// Update обновляет запись id значениями формы и файлами.
	Update func(ctx context.Context, id string, v FormValues, files map[string]FileInput) error
	// Delete удаляет запись по id.
	Delete func(ctx context.Context, id string) error
}
