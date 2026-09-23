-- +goose Up
CREATE TABLE candidate_request_bindings (
  app_name TEXT NOT NULL,
  request_id TEXT NOT NULL,
  app_id TEXT NOT NULL,
  deployment_id TEXT NOT NULL,
  created_at TEXT NOT NULL,
  PRIMARY KEY (app_name, request_id),
  FOREIGN KEY (app_name, request_id) REFERENCES deployment_requests(app_name, request_id),
  FOREIGN KEY (app_id) REFERENCES apps(id),
  FOREIGN KEY (deployment_id) REFERENCES deployments(id)
);

-- +goose Down
DROP TABLE candidate_request_bindings;
