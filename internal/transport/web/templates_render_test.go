package web

import (
	"html/template"
	"testing"
)

// Тест ниже ловит ошибки ШАБЛОНОВ, которые НЕ видит `go build`/`go vet`: вызов
// несуществующей функции (`function "seq" not defined`), битый `{{range}}`,
// обращение к несуществующему полю. Такие ошибки проявляются только при ПАРСЕ
// шаблона в рантайме — раньше они роняли приложение лишь при заходе на страницу
// (превью «не ответило»), хотя build/test были зелёными. Теперь падают на
// `make test`, до публикации.

// parseTemplates парсит набор шаблонов приложения из embed (тот же templatesFS и
// тот же вызов ParseFS, что использует сервер в проде — web.go NewServer). Ошибка
// парсинга (неизвестная функция, незакрытый action) возвращается здесь.
func parseTemplates() (*template.Template, error) {
	return template.ParseFS(templatesFS, "templates/*.html")
}

// TestTemplatesParse: все шаблоны парсятся. Ловит `function "..." not defined` и
// синтаксические ошибки — ровно то, что убивало страницу в рантайме.
func TestTemplatesParse(t *testing.T) {
	if _, err := parseTemplates(); err != nil {
		t.Fatalf("шаблоны не парсятся (та же ошибка уронила бы страницу в рантайме): %v", err)
	}
}
