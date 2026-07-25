// Package router — маленький HTTP-роутер с паттернами вида «METHOD /path/{id}».
//
// API повторяет net/http.ServeMux (Handle/HandleFunc/ServeHTTP), паттерны — те
// же, что в стандартной библиотеке: метод, сегменты-литералы, {параметры}
// (доступны через r.PathValue) и подветки с хвостовым «/». Зачем свой: матчинг
// не должен зависеть от окружения, в котором собран ХОСТ-процесс — приложение
// живёт и обычным сервером, и плагином серверлесс-функции, и роутинг обязан
// вести себя одинаково везде.
package router

import (
	"net/http"
	"strings"
)

// route — один зарегистрированный паттерн.
type route struct {
	method   string   // "" = любой метод
	segments []string // сегменты пути; "{name}" — параметр
	subtree  bool     // паттерн оканчивался на "/" — матчит всю подветку
	handler  http.Handler
}

// Mux — роутер. Создавать через New.
type Mux struct {
	routes []route
}

// New создаёт пустой роутер.
func New() *Mux { return &Mux{} }

// HandleFunc регистрирует обработчик для паттерна («GET /notes/{id}»,
// «POST /notes», «GET /static/», «GET /»).
func (m *Mux) HandleFunc(pattern string, h func(http.ResponseWriter, *http.Request)) {
	m.Handle(pattern, http.HandlerFunc(h))
}

// Handle регистрирует http.Handler для паттерна.
func (m *Mux) Handle(pattern string, h http.Handler) {
	method := ""
	path := pattern
	if i := strings.IndexByte(pattern, ' '); i > 0 {
		method = pattern[:i]
		path = strings.TrimLeft(pattern[i:], " ")
	}
	subtree := strings.HasSuffix(path, "/")
	trimmed := strings.Trim(path, "/")
	var segs []string
	if trimmed != "" {
		segs = strings.Split(trimmed, "/")
	}
	m.routes = append(m.routes, route{method: method, segments: segs, subtree: subtree, handler: h})
}

// ServeHTTP находит самый специфичный подходящий маршрут. Совпал путь, но не
// метод — 405 с заголовком Allow. Ничего не совпало — 404.
func (m *Mux) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	segs := splitPath(r.URL.Path)

	var best *route
	var bestParams map[string]string
	var allowed []string

	for i := range m.routes {
		rt := &m.routes[i]
		params, ok := rt.match(segs)
		if !ok {
			continue
		}
		if rt.method != "" && rt.method != r.Method {
			allowed = append(allowed, rt.method)
			continue
		}
		if best == nil || rt.moreSpecific(best) {
			best, bestParams = rt, params
		}
	}

	if best != nil {
		for k, v := range bestParams {
			r.SetPathValue(k, v)
		}
		best.handler.ServeHTTP(w, r)
		return
	}
	if len(allowed) > 0 {
		w.Header().Set("Allow", strings.Join(allowed, ", "))
		http.Error(w, "405 method not allowed", http.StatusMethodNotAllowed)
		return
	}
	http.NotFound(w, r)
}

// match сообщает, накрывает ли маршрут путь, и возвращает значения параметров.
func (rt *route) match(segs []string) (map[string]string, bool) {
	if rt.subtree {
		// Подветка: путь должен НАЧИНАТЬСЯ с сегментов паттерна.
		if len(segs) < len(rt.segments) {
			return nil, false
		}
	} else if len(segs) != len(rt.segments) {
		return nil, false
	}
	var params map[string]string
	for i, ps := range rt.segments {
		if strings.HasPrefix(ps, "{") && strings.HasSuffix(ps, "}") {
			if params == nil {
				params = map[string]string{}
			}
			params[ps[1:len(ps)-1]] = segs[i]
			continue
		}
		if ps != segs[i] {
			return nil, false
		}
	}
	return params, true
}

// moreSpecific — приоритет: больше сегментов > точный (не подветка) > меньше
// параметров. Даёт стандартную интуицию «/notes/new важнее /notes/{id} важнее /».
func (rt *route) moreSpecific(other *route) bool {
	if len(rt.segments) != len(other.segments) {
		return len(rt.segments) > len(other.segments)
	}
	if rt.subtree != other.subtree {
		return !rt.subtree
	}
	return rt.paramCount() < other.paramCount()
}

func (rt *route) paramCount() int {
	n := 0
	for _, s := range rt.segments {
		if strings.HasPrefix(s, "{") {
			n++
		}
	}
	return n
}

func splitPath(p string) []string {
	trimmed := strings.Trim(p, "/")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "/")
}
