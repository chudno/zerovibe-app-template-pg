-- Схема заметок. Файл кладётся как
-- internal/platform/pg/migrations/NNNNN_<имя>.sql (номер — следующий по порядку).
-- Правила:
--   * id — bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY (выдаёт база);
--   * время — timestamptz, NULL = «не было»;
--   * владелец — REFERENCES users (id), каскад по вкусу задачи;
--   * индекс на колонки, по которым ищем не по первичному ключу.

CREATE TABLE notes (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    owner_id   bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    title      text NOT NULL,
    body       text NOT NULL DEFAULT '',
    due_date   text,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_notes_owner ON notes (owner_id);
