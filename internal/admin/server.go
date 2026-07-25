package admin

import (
	"embed"
	"encoding/csv"
	"errors"
	"html/template"
	"net/http"
	"strconv"
	"strings"

	"github.com/chudno/zerovibe/internal/domain"
	"github.com/chudno/zerovibe/internal/usecase"
)

//go:embed templates/*.html
var templatesFS embed.FS

// maxUploadBytes — потолок размера одной формы с файлами (16 МиБ). Защита от
// переполнения памяти на парсинге multipart.
const maxUploadBytes = 16 << 20

// Server — generic админ-сервер. Держит реестр сущностей, файловое хранилище и свои
// шаблоны. Монтируется веб-сервером приложения под префиксом /admin (см. Routes).
type Server struct {
	reg   *Registry
	files usecase.FileStore
	tmpl  *template.Template
}

// NewServer собирает админ-сервер. files может быть nil (тогда file-поля не работают,
// но остальная админка функционирует). Возвращает nil-безопасную ошибку парсинга шаблонов.
func NewServer(reg *Registry, files usecase.FileStore) (*Server, error) {
	tmpl, err := template.New("admin").Funcs(adminFuncs).ParseFS(templatesFS, "templates/*.html")
	if err != nil {
		return nil, err
	}
	return &Server{reg: reg, files: files, tmpl: tmpl}, nil
}

// loginView — данные формы входа в админку.
type loginView struct {
	Email string
	Err   string
}

// RenderLogin рисует отдельную страницу входа в админку (свой дизайн). Вызывается из
// web-сервера (у него auth/сессии); сам admin аутентификацию не делает, только вид.
func (s *Server) RenderLogin(w http.ResponseWriter, email, errMsg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "login", loginView{Email: email, Err: errMsg}); err != nil {
		http.Error(w, "внутренняя ошибка админки", http.StatusInternalServerError)
	}
}

// HasResources сообщает, есть ли в админке хоть одна сущность (для решения, монтировать
// ли вход в админку в web-сервере).
func (s *Server) HasResources() bool { return !s.reg.Empty() }

// Routable — то, на чём админка умеет регистрироваться (роутер приложения).
type Routable interface {
	HandleFunc(pattern string, handler func(http.ResponseWriter, *http.Request))
}

// Mount регистрирует админ-маршруты на переданном роутере под префиксом /admin.
// guard оборачивает каждый хендлер проверкой роли (передаётся из web-сервера, чтобы
// admin не дублировал логику аутентификации — она в одном месте приложения).
func (s *Server) Mount(mux Routable, guard func(http.HandlerFunc) http.HandlerFunc) {
	if s.reg.Empty() {
		return // нет сущностей — админка не монтируется
	}
	mux.HandleFunc("GET /admin", guard(s.handleHome))
	mux.HandleFunc("GET /admin/{resource}", guard(s.handleList))
	mux.HandleFunc("GET /admin/{resource}/export", guard(s.handleExport))
	mux.HandleFunc("GET /admin/{resource}/new", guard(s.handleNew))
	mux.HandleFunc("POST /admin/{resource}", guard(s.handleCreate))
	mux.HandleFunc("GET /admin/{resource}/{id}/edit", guard(s.handleEdit))
	mux.HandleFunc("PUT /admin/{resource}/{id}", guard(s.handleUpdate))
	mux.HandleFunc("DELETE /admin/{resource}/{id}", guard(s.handleDelete))
}

// resourceParam достаёт дескриптор из пути или пишет 404.
func (s *Server) resourceParam(w http.ResponseWriter, r *http.Request) (ResourceHandle, bool) {
	h, ok := s.reg.Get(r.PathValue("resource"))
	if !ok {
		http.NotFound(w, r)
		return nil, false
	}
	return h, true
}

// handleHome редиректит на первый раздел (у админки нет отдельной «главной»).
func (s *Server) handleHome(w http.ResponseWriter, r *http.Request) {
	all := s.reg.All()
	if len(all) == 0 {
		http.NotFound(w, r)
		return
	}
	http.Redirect(w, r, "/admin/"+all[0].Name(), http.StatusSeeOther)
}

// handleList рендерит экран списка: поиск/фильтры/сортировка/пагинация.
func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	h, ok := s.resourceParam(w, r)
	if !ok {
		return
	}
	records, err := h.List(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	q := parseListQuery(r)
	win := q.apply(h, records)
	view := s.listView(r.Context(), h, win, q)

	// Что вернуть, зависит от того, КУДА htmx подменяет (заголовок HX-Target):
	//  - admin-content → переход по разделу из сайдбара: контент раздела (топбар+список);
	//  - listbody      → поиск/фильтр/сортировка/пагинация: только секция списка;
	//  - нет HX        → прямой заход по URL: полная страница.
	// Всё без перезагрузки страницы (кроме первого прямого захода).
	if isHX(r) {
		switch r.Header.Get("HX-Target") {
		case "admin-content":
			s.render(w, "content", view)
		default:
			s.render(w, "listbody", view)
		}
		return
	}
	s.render(w, "list", view)
}

// handleExport отдаёт CSV всех (отфильтрованных) записей раздела.
func (s *Server) handleExport(w http.ResponseWriter, r *http.Request) {
	h, ok := s.resourceParam(w, r)
	if !ok {
		return
	}
	records, err := h.List(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	q := parseListQuery(r)
	records = filterRecords(h, records, q)

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename="+h.Name()+".csv")
	cw := csv.NewWriter(w)
	defer cw.Flush()

	var header []string
	var cols []Field
	for _, f := range h.Fields() {
		if f.InList {
			header = append(header, f.Label)
			cols = append(cols, f)
		}
	}
	_ = cw.Write(header)
	for _, rec := range records {
		row := make([]string, 0, len(cols))
		for _, f := range cols {
			c := rec.Cells[f.Name]
			val := c.Display
			if val == "" {
				val = c.Value
			}
			row = append(row, val)
		}
		_ = cw.Write(row)
	}
}

// handleNew отдаёт форму создания как модалку (htmx подгружает её в контейнер без
// перезагрузки). Прямой заход по URL → форма на полной странице (fallback).
func (s *Server) handleNew(w http.ResponseWriter, r *http.Request) {
	h, ok := s.resourceParam(w, r)
	if !ok {
		return
	}
	s.renderForm(w, r, s.formView(r.Context(), h, "", FormValues{}, ""))
}

// handleCreate обрабатывает отправку формы создания. Успех → закрыть модалку и обновить
// список (через HX-Trigger), без перезагрузки. Ошибка → форма с подписью ошибки обратно.
func (s *Server) handleCreate(w http.ResponseWriter, r *http.Request) {
	h, ok := s.resourceParam(w, r)
	if !ok {
		return
	}
	values, files, err := s.parseForm(r, h)
	if err == nil {
		err = h.Create(r.Context(), values, files)
	}
	if err != nil {
		s.renderForm(w, r, s.formView(r.Context(), h, "", values, errText(err)))
		return
	}
	s.afterMutation(w, r, h)
}

// handleEdit отдаёт форму редактирования (модалка) с текущими значениями записи.
func (s *Server) handleEdit(w http.ResponseWriter, r *http.Request) {
	h, ok := s.resourceParam(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	values, err := h.Get(r.Context(), id)
	if err != nil {
		s.fail(w, err)
		return
	}
	s.renderForm(w, r, s.formView(r.Context(), h, id, values, ""))
}

// handleUpdate обрабатывает отправку формы редактирования (аналогично create).
func (s *Server) handleUpdate(w http.ResponseWriter, r *http.Request) {
	h, ok := s.resourceParam(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	values, files, err := s.parseForm(r, h)
	if err == nil {
		err = h.Update(r.Context(), id, values, files)
	}
	if err != nil {
		s.renderForm(w, r, s.formView(r.Context(), h, id, values, errText(err)))
		return
	}
	s.afterMutation(w, r, h)
}

// handleDelete удаляет запись. HTMX → обновлённый список без перезагрузки; при ошибке
// валидации (напр. последний админ) — 422 с текстом, который htmx покажет.
func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	h, ok := s.resourceParam(w, r)
	if !ok {
		return
	}
	if err := h.Delete(r.Context(), r.PathValue("id")); err != nil {
		s.fail(w, err)
		return
	}
	s.afterMutation(w, r, h)
}

// afterMutation отдаёт результат успешной мутации БЕЗ перезагрузки страницы.
//
// Удаление (delete) таргетит #listbody напрямую → возвращаем свежую секцию списка.
// Create/Update таргетят #modal (туда же кладётся форма с ошибкой при провале) → при
// успехе очищаем модалку (пустое тело) и сигналим refreshList, чтобы клиент отдельно
// перезагрузил секцию списка. Для не-htmx (прямой POST) — редирект (fallback).
func (s *Server) afterMutation(w http.ResponseWriter, r *http.Request, h ResourceHandle) {
	if !isHX(r) {
		http.Redirect(w, r, "/admin/"+h.Name(), http.StatusSeeOther)
		return
	}
	if r.Method == http.MethodDelete {
		records, err := h.List(r.Context())
		if err != nil {
			s.fail(w, err)
			return
		}
		q := parseListQuery(r)
		win := q.apply(h, records)
		s.render(w, "listbody", s.listView(r.Context(), h, win, q))
		return
	}
	// create/update: закрыть модалку (пустой #modal) и попросить клиента обновить список.
	w.Header().Set("HX-Trigger", "refreshList")
	w.WriteHeader(http.StatusOK)
}

// renderForm отдаёт форму как модалку (htmx). Прямой заход по URL формы (без htmx) —
// редирект на список: форма открывается только поверх списка, отдельной страницы-формы
// нет (так интерфейс всегда остаётся «как SPA», без самостоятельных form-страниц).
func (s *Server) renderForm(w http.ResponseWriter, r *http.Request, v formView) {
	if !isHX(r) {
		http.Redirect(w, r, "/admin/"+v.Resource, http.StatusSeeOther)
		return
	}
	s.render(w, "formdialog", v)
}

// isHX сообщает, что запрос пришёл от htmx (заголовок HX-Request: true).
func isHX(r *http.Request) bool { return r.Header.Get("HX-Request") == "true" }

// parseForm разбирает форму (multipart, если есть файлы): текстовые значения по полям
// + загруженные файлы. Файлы сохраняются в FileStore сразу, в values кладётся ключ.
func (s *Server) parseForm(r *http.Request, h ResourceHandle) (FormValues, map[string]FileInput, error) {
	if err := r.ParseMultipartForm(maxUploadBytes); err != nil && !errors.Is(err, http.ErrNotMultipart) {
		return FormValues{}, nil, err
	}
	values := FormValues{}
	files := map[string]FileInput{}
	for _, f := range h.Fields() {
		if f.Type == FieldFile {
			if s.files == nil {
				continue
			}
			fh, header, ferr := r.FormFile(f.Name)
			if ferr != nil || header == nil {
				// файл не приложили — оставляем прежний (ключ передаётся скрытым полем)
				values[f.Name] = r.FormValue(f.Name + "_key")
				continue
			}
			key, serr := s.files.Save(r.Context(), header.Filename, fh, header.Size)
			_ = fh.Close()
			if serr != nil {
				return values, files, serr
			}
			values[f.Name] = key
			files[f.Name] = FileInput{FileName: header.Filename, Size: header.Size}
			continue
		}
		if f.Type == FieldBool {
			// чекбокс/switch: присутствие → "true"
			if r.FormValue(f.Name) != "" {
				values[f.Name] = "true"
			} else {
				values[f.Name] = "false"
			}
			continue
		}
		values[f.Name] = strings.TrimSpace(r.FormValue(f.Name))
	}
	return values, files, nil
}

// parseListQuery собирает ListQuery из query-string запроса.
func parseListQuery(r *http.Request) ListQuery {
	q := ListQuery{
		Search:   r.URL.Query().Get("q"),
		Filters:  map[string]string{},
		SortBy:   r.URL.Query().Get("sort"),
		SortDir:  r.URL.Query().Get("dir"),
		PageSize: 20,
	}
	if q.SortDir != "desc" {
		q.SortDir = "asc"
	}
	q.Page, _ = strconv.Atoi(r.URL.Query().Get("page"))
	if q.Page < 1 {
		q.Page = 1
	}
	for k, v := range r.URL.Query() {
		if strings.HasPrefix(k, "f_") && len(v) > 0 {
			q.Filters[strings.TrimPrefix(k, "f_")] = v[0]
		}
	}
	return q
}

// render выполняет именованный шаблон с данными.
func (s *Server) render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, name, data); err != nil {
		http.Error(w, "внутренняя ошибка админки", http.StatusInternalServerError)
	}
}

// fail мапит ошибку в HTTP-код: доменная валидация/защита → 422, не найдено → 404,
// прочее → 500. Чтобы «нельзя удалить последнего админа» не выглядело сбоем сервера.
func (s *Server) fail(w http.ResponseWriter, err error) {
	var validation domain.ErrValidation
	var notFound domain.ErrNotFound
	switch {
	case errors.As(err, &validation):
		http.Error(w, errText(err), http.StatusUnprocessableEntity)
	case errors.As(err, &notFound):
		http.Error(w, errText(err), http.StatusNotFound)
	default:
		http.Error(w, errText(err), http.StatusInternalServerError)
	}
}

// errText извлекает человекочитаемый текст ошибки (для подписи в форме).
func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
