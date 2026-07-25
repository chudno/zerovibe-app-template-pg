package admin_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/chudno/zerovibe/internal/admin"
	"github.com/chudno/zerovibe/internal/domain"
)

// item — тестовая сущность для проверки generic-механики админки.
type item struct {
	ID    int64
	Name  string
	Score int
	Done  bool
}

// memStore — in-memory «репозиторий» тестовой сущности.
type memStore struct {
	seq   int64
	items map[int64]item
}

func newStore() *memStore { return &memStore{items: map[int64]item{}} }

func (s *memStore) add(name string, score int, done bool) {
	s.seq++
	s.items[s.seq] = item{ID: s.seq, Name: name, Score: score, Done: done}
}

// registerItems регистрирует тестовую сущность в реестре (повторяет структуру
// настоящего дескриптора: поля + аксессоры).
func registerItems(reg *admin.Registry, s *memStore) {
	admin.Register(reg, admin.Resource[item]{
		Name:  "items",
		Title: "Элементы",
		Fields: []admin.Field{
			{Name: "name", Label: "Имя", Type: admin.FieldText, Required: true, InList: true, Sortable: true},
			{Name: "score", Label: "Очки", Type: admin.FieldNumber, InList: true, Sortable: true},
			{Name: "done", Label: "Готово", Type: admin.FieldBool, InList: true},
		},
		Row: func(it item) admin.Record {
			return admin.Record{
				ID: strconv.FormatInt(it.ID, 10),
				Cells: map[string]admin.Cell{
					"name":  {Value: it.Name, Display: it.Name},
					"score": {Value: strconv.Itoa(it.Score), Display: strconv.Itoa(it.Score)},
					"done":  {Value: strconv.FormatBool(it.Done)},
				},
			}
		},
		List: func(_ context.Context) ([]item, error) {
			out := make([]item, 0, len(s.items))
			for _, it := range s.items {
				out = append(out, it)
			}
			return out, nil
		},
		Get: func(_ context.Context, id string) (admin.FormValues, error) {
			iid, _ := strconv.ParseInt(id, 10, 64)
			it, ok := s.items[iid]
			if !ok {
				return nil, domain.ErrNotFound{Entity: "item", ID: iid}
			}
			return admin.FormValues{"name": it.Name, "score": strconv.Itoa(it.Score)}, nil
		},
		Create: func(_ context.Context, v admin.FormValues, _ map[string]admin.FileInput) error {
			if strings.TrimSpace(v["name"]) == "" {
				return domain.ErrValidation{Field: "name", Msg: "имя обязательно"}
			}
			sc, _ := strconv.Atoi(v["score"])
			s.add(v["name"], sc, v["done"] == "true")
			return nil
		},
		Update: func(_ context.Context, id string, v admin.FormValues, _ map[string]admin.FileInput) error {
			iid, _ := strconv.ParseInt(id, 10, 64)
			it, ok := s.items[iid]
			if !ok {
				return domain.ErrNotFound{Entity: "item", ID: iid}
			}
			it.Name = v["name"]
			it.Score, _ = strconv.Atoi(v["score"])
			s.items[iid] = it
			return nil
		},
		Delete: func(_ context.Context, id string) error {
			iid, _ := strconv.ParseInt(id, 10, 64)
			delete(s.items, iid)
			return nil
		},
	})
}

// newAdminHandler собирает смонтированную админку на тестовом реестре. guard пропускает
// всех (роль проверяется в web-слое, тут не интересна).
func newAdminHandler(t *testing.T, s *memStore) http.Handler {
	t.Helper()
	reg := admin.NewRegistry()
	registerItems(reg, s)
	srv, err := admin.NewServer(reg, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	mux := http.NewServeMux()
	srv.Mount(mux, func(h http.HandlerFunc) http.HandlerFunc { return h })
	return mux
}

func TestAdminList_ShowsRecords(t *testing.T) {
	s := newStore()
	s.add("Альфа", 10, true)
	s.add("Бета", 5, false)
	h := newAdminHandler(t, s)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/admin/items", nil))
	if rec.Code != 200 {
		t.Fatalf("список: код %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Альфа") || !strings.Contains(body, "Бета") {
		t.Fatal("список не показывает записи")
	}
	if !strings.Contains(body, "Элементы") {
		t.Fatal("нет заголовка раздела")
	}
}

func TestAdminList_SearchAndSort(t *testing.T) {
	s := newStore()
	s.add("Яблоко", 1, false)
	s.add("Банан", 2, false)
	h := newAdminHandler(t, s)

	// поиск
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/admin/items?q=ябл", nil))
	body := rec.Body.String()
	if !strings.Contains(body, "Яблоко") || strings.Contains(body, "Банан") {
		t.Fatal("поиск не отфильтровал")
	}

	// сортировка по числу (score desc): Банан(2) выше Яблоко(1)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/admin/items?sort=score&dir=desc", nil))
	body = rec.Body.String()
	if strings.Index(body, "Банан") > strings.Index(body, "Яблоко") {
		t.Fatal("сортировка по числу desc не сработала")
	}
}

func TestAdminExport_CSV(t *testing.T) {
	s := newStore()
	s.add("Один", 100, true)
	h := newAdminHandler(t, s)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/admin/items/export", nil))
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/csv") {
		t.Fatalf("CSV content-type = %q", ct)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Имя,Очки,Готово") || !strings.Contains(body, "Один,100") {
		t.Fatalf("CSV неверный:\n%s", body)
	}
}

func TestAdminCreate_ThenList(t *testing.T) {
	s := newStore()
	h := newAdminHandler(t, s)

	form := url.Values{"name": {"Новый"}, "score": {"7"}}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/admin/items", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("создание: код %d (ожидался 303)", rec.Code)
	}
	if len(s.items) != 1 {
		t.Fatalf("после создания записей: %d", len(s.items))
	}
}

func TestAdminCreate_ValidationError(t *testing.T) {
	s := newStore()
	h := newAdminHandler(t, s)

	// форма всегда отправляется htmx (модалка): при ошибке возвращается модалка с
	// подписью ошибки (200), запись не создаётся.
	form := url.Values{"name": {""}, "score": {"1"}} // пустое имя
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/admin/items", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("валидация: код %d (ожидался 200 с ошибкой)", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "имя обязательно") {
		t.Fatal("нет текста ошибки в форме")
	}
	if !strings.Contains(rec.Body.String(), "modal-overlay") {
		t.Fatal("ошибка должна вернуться в модалку формы")
	}
	if len(s.items) != 0 {
		t.Fatal("запись не должна была создаться")
	}
}

func TestAdminDelete(t *testing.T) {
	s := newStore()
	s.add("Удалить", 1, false)
	h := newAdminHandler(t, s)

	// без htmx → редирект (fallback)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("DELETE", "/admin/items/1", nil))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("удаление: код %d", rec.Code)
	}
	if len(s.items) != 0 {
		t.Fatal("запись не удалена")
	}
}

// TestAdminHTMX_NoReload проверяет SPA-поведение: HX-запросы возвращают ФРАГМЕНТЫ
// (без <html>/layout) и сигналы, а не полные страницы/редиректы.
func TestAdminHTMX_NoReload(t *testing.T) {
	s := newStore()
	s.add("Один", 1, false)
	h := newAdminHandler(t, s)

	hx := func(method, path string, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("HX-Request", "true")
		if body != "" {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	// список по htmx → фрагмент listbody, БЕЗ <html>
	rec := hx("GET", "/admin/items", "")
	if strings.Contains(rec.Body.String(), "<html") {
		t.Fatal("htmx-список не должен содержать <html> (это перезагрузка, не фрагмент)")
	}
	if !strings.Contains(rec.Body.String(), "class=\"toolbar\"") {
		t.Fatal("htmx-список должен содержать секцию списка")
	}

	// форма по htmx → модалка, БЕЗ <html>
	rec = hx("GET", "/admin/items/new", "")
	if strings.Contains(rec.Body.String(), "<html") || !strings.Contains(rec.Body.String(), "modal-overlay") {
		t.Fatal("htmx-форма должна быть модалкой без <html>")
	}

	// создание по htmx → НЕ редирект (303), а 200 + HX-Trigger refreshList
	rec = hx("POST", "/admin/items", "name=Два&score=2")
	if rec.Code != 200 {
		t.Fatalf("htmx-создание: код %d (ожидался 200, не редирект)", rec.Code)
	}
	if rec.Header().Get("HX-Trigger") != "refreshList" {
		t.Fatalf("htmx-создание должно слать HX-Trigger refreshList, got %q", rec.Header().Get("HX-Trigger"))
	}

	// удаление по htmx → фрагмент listbody (без <html>), не редирект
	rec = hx("DELETE", "/admin/items/1", "")
	if rec.Code != 200 || strings.Contains(rec.Body.String(), "<html") {
		t.Fatalf("htmx-удаление: код %d, должно быть 200 + фрагмент", rec.Code)
	}
}

func TestAdminUnknownResource_404(t *testing.T) {
	s := newStore()
	h := newAdminHandler(t, s)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/admin/ghosts", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("неизвестный раздел: код %d (ожидался 404)", rec.Code)
	}
}

func TestEmptyRegistry_NotMounted(t *testing.T) {
	reg := admin.NewRegistry()
	srv, err := admin.NewServer(reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	srv.Mount(mux, func(h http.HandlerFunc) http.HandlerFunc { return h })
	// при пустом реестре маршруты не регистрируются → 404 от mux
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/admin", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("пустой реестр: код %d (ожидался 404)", rec.Code)
	}
}
