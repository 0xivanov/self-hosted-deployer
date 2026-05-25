-- +goose Up
CREATE TABLE events (
  id TEXT PRIMARY KEY,
  created_at TEXT NOT NULL,
  type TEXT NOT NULL,
  severity TEXT NOT NULL,
  message TEXT NOT NULL,
  app_id TEXT,
  node_id TEXT,
  deployment_id TEXT,
  metadata_json TEXT NOT NULL DEFAULT '{}'
);

CREATE INDEX events_created_at_idx ON events (created_at DESC, id DESC);
CREATE INDEX events_app_id_created_at_idx ON events (app_id, created_at DESC);
CREATE INDEX events_node_id_created_at_idx ON events (node_id, created_at DESC);
CREATE INDEX events_type_created_at_idx ON events (type, created_at DESC);
CREATE INDEX events_severity_created_at_idx ON events (severity, created_at DESC);

-- +goose Down
DROP INDEX events_severity_created_at_idx;
DROP INDEX events_type_created_at_idx;
DROP INDEX events_node_id_created_at_idx;
DROP INDEX events_app_id_created_at_idx;
DROP INDEX events_created_at_idx;
DROP TABLE events;
