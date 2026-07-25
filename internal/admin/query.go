package admin

import (
	"strconv"
	"strings"
)

// compareCells сравнивает два значения ячеек для сортировки. Числовые поля — как
// числа (иначе "10" < "9" лексикографически), остальные — регистронезависимо строкой.
func compareCells(a, b string, t FieldType) bool {
	if t == FieldNumber {
		af, aerr := strconv.ParseFloat(strings.TrimSpace(a), 64)
		bf, berr := strconv.ParseFloat(strings.TrimSpace(b), 64)
		if aerr == nil && berr == nil {
			return af < bf
		}
	}
	return strings.ToLower(a) < strings.ToLower(b)
}

// ListQuery — параметры запроса списка (из query-string): поиск, фильтры, сортировка,
// страница. Generic-слой применяет их к записям ПОСЛЕ List() (фильтрация в памяти —
// объёмы данных приложения небольшие; точечная оптимизация — потом в репозитории).
type ListQuery struct {
	Search   string            // подстрока поиска по текстовым полям
	Filters  map[string]string // field name -> выбранное значение (select/badge)
	SortBy   string            // имя поля сортировки
	SortDir  string            // "asc" | "desc"
	Page     int               // 1-based
	PageSize int
}

// pageWindow — результат пагинации: срез записей текущей страницы и метаданные.
type pageWindow struct {
	Records  []Record
	Page     int
	Pages    int
	Total    int
	PageSize int
}

// apply фильтрует, сортирует и нарезает записи на страницу по запросу q и полям h.
func (q ListQuery) apply(h ResourceHandle, records []Record) pageWindow {
	records = filterRecords(h, records, q)
	if q.SortBy != "" {
		if f, ok := fieldByName(h, q.SortBy); ok && f.Sortable {
			sortRecords(records, f, q.SortDir)
		}
	}

	total := len(records)
	size := q.PageSize
	if size <= 0 {
		size = 20
	}
	pages := (total + size - 1) / size
	if pages == 0 {
		pages = 1
	}
	page := q.Page
	if page < 1 {
		page = 1
	}
	if page > pages {
		page = pages
	}
	start := (page - 1) * size
	end := start + size
	if start > total {
		start = total
	}
	if end > total {
		end = total
	}
	return pageWindow{
		Records:  records[start:end],
		Page:     page,
		Pages:    pages,
		Total:    total,
		PageSize: size,
	}
}

// filterRecords применяет поиск (по InList-текстовым полям) и точные фильтры.
func filterRecords(h ResourceHandle, records []Record, q ListQuery) []Record {
	search := strings.ToLower(strings.TrimSpace(q.Search))
	if search == "" && len(q.Filters) == 0 {
		return records
	}
	out := make([]Record, 0, len(records))
	for _, rec := range records {
		if search != "" && !recordMatchesSearch(h, rec, search) {
			continue
		}
		if !recordMatchesFilters(rec, q.Filters) {
			continue
		}
		out = append(out, rec)
	}
	return out
}

// recordMatchesSearch — true, если поиск-подстрока встречается в любой показываемой
// в списке текстовой ячейке (по Display — то, что видит пользователь).
func recordMatchesSearch(h ResourceHandle, rec Record, search string) bool {
	for _, f := range h.Fields() {
		if !f.InList {
			continue
		}
		c := rec.Cells[f.Name]
		if strings.Contains(strings.ToLower(c.Display), search) || strings.Contains(strings.ToLower(c.Value), search) {
			return true
		}
	}
	return false
}

// recordMatchesFilters — true, если запись удовлетворяет всем точным фильтрам.
func recordMatchesFilters(rec Record, filters map[string]string) bool {
	for name, want := range filters {
		if want == "" {
			continue
		}
		if rec.Cells[name].Value != want {
			return false
		}
	}
	return true
}
