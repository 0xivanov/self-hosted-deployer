-- +goose Up
CREATE TABLE registry_credentials (
  app_name TEXT NOT NULL,
  revision TEXT NOT NULL,
  registry TEXT NOT NULL CHECK (registry IN ('docker.io', 'ghcr.io')),
  ciphertext TEXT NOT NULL,
  created_at TEXT NOT NULL,
  PRIMARY KEY (app_name, revision)
);

-- +goose Down
DROP TABLE registry_credentials;
