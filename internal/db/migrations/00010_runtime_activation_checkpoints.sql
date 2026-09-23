-- +goose Up
CREATE TABLE runtime_activation_checkpoints (
  app_name TEXT NOT NULL,
  request_id TEXT NOT NULL,
  stage TEXT NOT NULL CHECK (stage IN ('prepared', 'fenced', 'activated', 'recovering', 'withdrawn')),
  intent_json TEXT NOT NULL,
  gate_json TEXT NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  PRIMARY KEY (app_name, request_id),
  FOREIGN KEY (app_name, request_id) REFERENCES deployment_requests(app_name, request_id) ON DELETE CASCADE
);
-- +goose Down
DROP TABLE runtime_activation_checkpoints;
