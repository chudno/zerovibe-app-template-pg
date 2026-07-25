# Multi-stage сборка. Бинарь статический (CGO_ENABLED=0) и кладётся в
# минимальный distroless-образ без libc.
#
# Образ слушает :8080. Данные живут в PostgreSQL (env DATABASE_URL задаёт платформа при
# деплое) — контейнер полностью stateless, volume не нужен.

# --- build ---
FROM golang:1.25-alpine AS build
WORKDIR /src

# Кэш зависимостей отдельным слоем.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# CGO_ENABLED=0 → статический бинарь. -ldflags для уменьшения размера.
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" \
    -o /out/zerovibe ./cmd/server

# --- runtime ---
FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/zerovibe /app/zerovibe

ENV ADDR=:8080
EXPOSE 8080

ENTRYPOINT ["/app/zerovibe"]
