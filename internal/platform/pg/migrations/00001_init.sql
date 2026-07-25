-- Исходная схема приложения: аутентификация + настройки.
-- Правила схемы (соблюдай в НОВЫХ миграциях):
--   * обычный PostgreSQL: bigint GENERATED ALWAYS AS IDENTITY для id,
--     timestamptz для времени (NULL = «не было»), UNIQUE и внешние ключи
--     разрешены и приветствуются;
--   * индексы на колонки, по которым ищем не по первичному ключу.

CREATE TABLE users (
    id                bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    email             text NOT NULL UNIQUE,
    password_hash     text NOT NULL,
    role              text NOT NULL DEFAULT 'user',
    email_verified_at timestamptz,
    created_at        timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE sessions (
    token      text PRIMARY KEY,
    user_id    bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL
);
CREATE INDEX idx_sessions_user ON sessions (user_id);

CREATE TABLE password_resets (
    token      text PRIMARY KEY,
    user_id    bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    used_at    timestamptz
);
CREATE INDEX idx_resets_user ON password_resets (user_id);

CREATE TABLE email_verifications (
    token      text PRIMARY KEY,
    user_id    bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    used_at    timestamptz
);
CREATE INDEX idx_verifications_user ON email_verifications (user_id);

CREATE TABLE rate_counters (
    key          text PRIMARY KEY,
    window_start timestamptz NOT NULL,
    count        bigint NOT NULL DEFAULT 0
);

CREATE TABLE settings (
    key        text PRIMARY KEY,
    value      text NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);
