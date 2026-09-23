-- +goose Up
CREATE TABLE environment_bundles (
  app_name TEXT NOT NULL,
  revision TEXT NOT NULL,
  names_json TEXT NOT NULL,
  ciphertext TEXT NOT NULL,
  created_at TEXT NOT NULL,
  PRIMARY KEY (app_name, revision)
);

-- +goose Down
DROP TABLE environment_bundles;
