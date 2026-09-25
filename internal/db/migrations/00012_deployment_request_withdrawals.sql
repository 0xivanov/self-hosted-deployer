-- +goose Up
CREATE TABLE deployment_request_withdrawals (
  app_name TEXT NOT NULL,
  request_id TEXT NOT NULL,
  requested_at TEXT NOT NULL,
  PRIMARY KEY (app_name, request_id),
  FOREIGN KEY (app_name, request_id) REFERENCES deployment_requests(app_name, request_id)
);

-- +goose Down
DROP TABLE deployment_request_withdrawals;
