// Вход серверлесс-функции Yandex Cloud (прод и превью на платформе).
// Точка входа при создании функции: handler.Handler.
//
// Приложение собирается ОДИН раз на инстанс функции (sync.Once) и живёт между
// вызовами, пока вендор держит инстанс тёплым: подключение к PostgreSQL и миграции не
// повторяются на каждый запрос. Холодный старт = Build (открыть базу, применить
// миграции) + первый запрос.
//
// Локальная разработка и песочница агента функцию НЕ используют — там обычный
// сервер cmd/server (см. skill run). Оба входа делят одну сборку internal/app.
// Мы полагаемся на method-паттерны ServeMux («GET /x», Go ≥1.22). Рантайм
// вендора может включать режим совместимости httpmuxgo121, при котором такие
// паттерны трактуются как литеральные пути и ВСЕ маршруты отдают 404 —
// директива ниже принудительно возвращает современное поведение.
//go:debug httpmuxgo121=0

package main

import (
	"context"
	"fmt"
	"sync"

	"github.com/chudno/zerovibe/internal/app"
	"github.com/chudno/zerovibe/internal/platform/ycf"
)

var (
	once    sync.Once
	handler func(context.Context, []byte) ([]byte, error)
	initErr error
)

// main не вызывается никогда: функцию собирают как Go-plugin (точка входа —
// Handler), а локально запускается cmd/server. Пустышка нужна, чтобы пакет
// компилировался обычным `go build ./...` при проверках.
func main() {}

// Handler — обработчик HTTP-вызова функции (формат события — internal/platform/ycf).
func Handler(ctx context.Context, raw []byte) ([]byte, error) {
	once.Do(func() {
		h, _, err := app.Build(context.Background()) // cleanup не нужен: инстанс убивает вендор
		if err != nil {
			initErr = err
			return
		}
		handler = ycf.Wrap(h)
	})
	if initErr != nil {
		return nil, fmt.Errorf("инициализация приложения: %w", initErr)
	}
	return handler(ctx, raw)
}
