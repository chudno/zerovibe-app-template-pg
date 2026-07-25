package admin

import (
	"context"
	"html/template"
)

// adminFuncs — функции, доступные в админ-шаблонах.
var adminFuncs = template.FuncMap{
	// dict собирает map для передачи нескольких значений во вложенный шаблон.
	"dict": func(pairs ...any) map[string]any {
		m := map[string]any{}
		for i := 0; i+1 < len(pairs); i += 2 {
			k, _ := pairs[i].(string)
			m[k] = pairs[i+1]
		}
		return m
	},
	// icon рендерит inline-SVG Lucide-иконку: {{icon "pencil" 16}}.
	"icon": icon,
}

// menuItem — пункт бокового меню (раздел сущности).
type menuItem struct {
	Name   string
	Title  string
	Icon   string
	Active bool
}

// menu строит пункты меню из реестра, отмечая активный раздел.
func (s *Server) menu(active string) []menuItem {
	items := make([]menuItem, 0)
	for _, h := range s.reg.All() {
		items = append(items, menuItem{Name: h.Name(), Title: h.Title(), Icon: h.Icon(), Active: h.Name() == active})
	}
	return items
}

// column — заголовок колонки списка (с состоянием сортировки).
type column struct {
	Name       string
	Label      string
	Sortable   bool
	SortedAsc  bool
	SortedDesc bool
}

// filterView — выпадающий фильтр над таблицей (по select/badge-полю).
type filterView struct {
	Name     string
	Label    string
	Options  []Option
	Selected string
}

// listView — данные экрана списка.
type listView struct {
	Title    string
	Resource string
	Menu     []menuItem
	Columns  []column
	Filters  []filterView
	Rows     []rowView
	Search   string
	SortBy   string
	SortDir  string
	Page     int
	Pages    int
	Total    int
	From     int
	To       int
	HasPrev  bool
	HasNext  bool
	PrevPage int
	NextPage int
}

// rowView — строка таблицы: id и ячейки в порядке колонок.
type rowView struct {
	ID    string
	Cells []cellView
}

// cellView — ячейка таблицы (с уже подготовленным отображением).
type cellView struct {
	Display string
	Tone    string
	IsFile  bool
	FileURL string
	IsBool  bool
	Bool    bool
}

// listView собирает вью-модель списка из окна пагинации и запроса.
func (s *Server) listView(ctx context.Context, h ResourceHandle, win pageWindow, q ListQuery) listView {
	var cols []column
	var filters []filterView
	for _, f := range h.Fields() {
		if f.InList {
			cols = append(cols, column{
				Name: f.Name, Label: f.Label, Sortable: f.Sortable,
				SortedAsc:  q.SortBy == f.Name && q.SortDir == "asc",
				SortedDesc: q.SortBy == f.Name && q.SortDir == "desc",
			})
		}
		// Фильтры — по полям с фиксированными вариантами (select/badge).
		if (f.Type == FieldSelect || f.Type == FieldBadge) && len(f.Options) > 0 {
			filters = append(filters, filterView{
				Name: f.Name, Label: f.Label, Options: f.Options, Selected: q.Filters[f.Name],
			})
		}
	}

	rows := make([]rowView, 0, len(win.Records))
	for _, rec := range win.Records {
		cells := make([]cellView, 0, len(cols))
		for _, col := range cols {
			c := rec.Cells[col.Name]
			cv := cellView{Display: c.Display, Tone: c.Tone, IsFile: c.IsFile}
			if cv.Display == "" {
				cv.Display = c.Value
			}
			f, _ := fieldByName(h, col.Name)
			if f.Type == FieldBool {
				cv.IsBool = true
				cv.Bool = c.Value == "true"
			}
			if c.IsFile && c.Value != "" && s.files != nil {
				if url, err := s.files.URL(ctx, c.Value); err == nil {
					cv.FileURL = url
				}
			}
			cells = append(cells, cv)
		}
		rows = append(rows, rowView{ID: rec.ID, Cells: cells})
	}

	from := 0
	if win.Total > 0 {
		from = (win.Page-1)*win.PageSize + 1
	}
	to := from + len(win.Records) - 1
	if to < 0 {
		to = 0
	}
	return listView{
		Title:    h.Title(),
		Resource: h.Name(),
		Menu:     s.menu(h.Name()),
		Columns:  cols,
		Filters:  filters,
		Rows:     rows,
		Search:   q.Search,
		SortBy:   q.SortBy,
		SortDir:  q.SortDir,
		Page:     win.Page,
		Pages:    win.Pages,
		Total:    win.Total,
		From:     from,
		To:       to,
		HasPrev:  win.Page > 1,
		HasNext:  win.Page < win.Pages,
		PrevPage: win.Page - 1,
		NextPage: win.Page + 1,
	}
}

// fieldView — поле формы с текущим значением и (для связей) подгруженными вариантами.
type fieldView struct {
	Name     string
	Label    string
	Type     FieldType
	Required bool
	Help     string
	Value    string
	Options  []Option
	FileURL  string // для file-поля: ссылка на текущий загруженный файл
}

// formView — данные экрана формы (создание/редактирование).
type formView struct {
	Title    string
	Resource string
	Menu     []menuItem
	ID       string // пусто → создание; иначе редактирование
	Fields   []fieldView
	Err      string
}

// formView собирает вью-модель формы, подгружая варианты для связей и URL файлов.
func (s *Server) formView(ctx context.Context, h ResourceHandle, id string, values FormValues, errMsg string) formView {
	fields := make([]fieldView, 0, len(h.Fields()))
	for _, f := range h.Fields() {
		fv := fieldView{
			Name: f.Name, Label: f.Label, Type: f.Type,
			Required: f.Required, Help: f.Help, Value: values[f.Name],
			Options: f.Options,
		}
		if f.Type == FieldRelation && f.RelationOptions != nil {
			if opts, err := f.RelationOptions(ctx); err == nil {
				fv.Options = opts
			}
		}
		if f.Type == FieldFile && fv.Value != "" && s.files != nil {
			if url, err := s.files.URL(ctx, fv.Value); err == nil {
				fv.FileURL = url
			}
		}
		fields = append(fields, fv)
	}
	return formView{
		Title:    h.Title(),
		Resource: h.Name(),
		Menu:     s.menu(h.Name()),
		ID:       id,
		Fields:   fields,
		Err:      errMsg,
	}
}
