-- +goose Up
CREATE TABLE deployment_requests (
  app_name TEXT NOT NULL,
  request_id TEXT NOT NULL,
  state TEXT NOT NULL CHECK (state IN ('pending', 'applied', 'withdrawn')),
  requested_state TEXT NOT NULL,
  previous_app_id TEXT,
  previous_state TEXT,
  report_withdrawal INTEGER NOT NULL CHECK (report_withdrawal IN (0, 1)),
  response_json TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  CHECK ((state = 'pending' AND response_json IS NULL) OR (state IN ('applied', 'withdrawn') AND response_json IS NOT NULL)),
  PRIMARY KEY (app_name, request_id)
);
CREATE UNIQUE INDEX deployment_requests_one_pending ON deployment_requests(app_name) WHERE state = 'pending';
-- +goose Down
DROP TABLE deployment_requests;
