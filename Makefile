.PHONY: run dev build test vet check docker docker-run tidy clean pg-up pg-down

# Локальный PostgreSQL для разработки и тестов (docker; данные не переживают rm).
# Интеграционные тесты сами пропускаются, если он не поднят.
pg-up:
	docker start zv-pg-local 2>/dev/null || docker run -d --name zv-pg-local \
		-p 5432:5432 -e POSTGRES_PASSWORD=postgres \
		postgres:16-alpine
	@echo "жду готовности PostgreSQL..." && \
	for i in $$(seq 1 30); do \
		docker exec zv-pg-local pg_isready -U postgres >/dev/null 2>&1 && echo готов && exit 0; \
		sleep 1; \
	done; echo "PostgreSQL не поднялся за 30с" && exit 1

pg-down:
	docker rm -f zv-pg-local 2>/dev/null || true

# Локальный запуск (нужен запущенный PostgreSQL: make pg-up; целится в базу app —
# создаётся автоматически прогоном make check либо руками: createdb).
run:
	go run ./cmd/server

# Dev-режим live-reload: ZV_DEV=1 заставляет приложение читать html-шаблоны и
# статику С ДИСКА на каждый запрос (а не из вшитого embed). Правки .html видны
# сразу по F5, без пересборки бинаря. Правки .go требуют перезапуска (Ctrl-C +
# `make dev`) — бинарь надо собрать заново.
dev:
	ZV_DEV=1 SECURE_COOKIE=false go run ./cmd/server

# Проверка перед публикацией: сборка + статанализ + тесты.
check:
	go build ./... && go vet ./... && go test ./...

# Сборка бинаря.
build:
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/zerovibe ./cmd/server

# Тесты (unit + e2e, без сети)
test:
	go test ./...

vet:
	go vet ./...

# Сборка Docker-образа
docker:
	docker build -t zerovibe:local .

# Запуск контейнера с volume под данные (порт 8080)
docker-run: docker
	docker run --rm -p 8080:8080 -v zerovibe-data:/data zerovibe:local

tidy:
	go mod tidy

clean:
	rm -rf bin zerovibe.db zerovibe.db-wal zerovibe.db-shm
