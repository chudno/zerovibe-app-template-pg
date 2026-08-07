package web

import (
	"bytes"
	"html/template"
	"strings"
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

// TestAppNameSingleSource: название приложения приходит на страницы ТОЛЬКО из
// настройки app_name ({{.AppName}}). Рендерим каждую страницу с подменённым
// именем: если в HTML остался плейсхолдер «Приложение» — значит название
// захардкожено в шаблоне (или забыт {{.AppName}}), и в проде владелец увидит
// «Приложение» на входе/регистрации/в лого, как бы он ни переименовывал продукт.
func TestAppNameSingleSource(t *testing.T) {
	tpl, err := parseTemplates()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pages := []string{"login", "register", "forgot", "reset", "settings", "verify"}
	for _, page := range pages {
		var buf bytes.Buffer
		data := pageData{AppName: "ИмяИзНастройки", Page: page, AllowSignup: true}
		if err := tpl.ExecuteTemplate(&buf, "layout", data); err != nil {
			t.Fatalf("render %q: %v", page, err)
		}
		html := buf.String()
		if strings.Contains(html, "Приложение") {
			t.Errorf("страница %q содержит захардкоженный плейсхолдер «Приложение» — название берётся ТОЛЬКО из настройки app_name (internal/domain/setting.go), в шаблонах — {{.AppName}}", page)
		}
		if !strings.Contains(html, "ИмяИзНастройки") {
			t.Errorf("страница %q не выводит {{.AppName}} — название приложения потеряно", page)
		}
	}
}
