-- +goose Up
ALTER TABLE routes ADD COLUMN status TEXT NOT NULL DEFAULT 'pending';
ALTER TABLE routes ADD COLUMN tls_enabled INTEGER NOT NULL DEFAULT 0;

CREATE UNIQUE INDEX routes_app_id_unique
  ON routes (app_id);

-- +goose Down
DROP INDEX routes_app_id_unique;
ALTER TABLE routes DROP COLUMN tls_enabled;
ALTER TABLE routes DROP COLUMN status;
