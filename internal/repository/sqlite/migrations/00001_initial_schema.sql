-- +goose Up
CREATE TABLE admin_tokens (
  token_hash TEXT PRIMARY KEY,
  display_name TEXT NOT NULL,
  created_at TEXT NOT NULL,
  last_used_at TEXT,
  revoked_at TEXT
);

CREATE TABLE nodes (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL UNIQUE,
  status TEXT NOT NULL,
  labels_json TEXT NOT NULL DEFAULT '{}',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE node_join_tokens (
  token_hash TEXT PRIMARY KEY,
  intended_node_name TEXT,
  labels_json TEXT NOT NULL DEFAULT '{}',
  created_at TEXT NOT NULL,
  expires_at TEXT NOT NULL,
  used_at TEXT
);

CREATE TABLE agent_tokens (
  token_hash TEXT PRIMARY KEY,
  node_id TEXT NOT NULL,
  created_at TEXT NOT NULL,
  last_used_at TEXT,
  revoked_at TEXT
);

CREATE TABLE apps (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL UNIQUE,
  image TEXT NOT NULL,
  desired_state_json TEXT NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  deleted_at TEXT
);

CREATE TABLE deployments (
  id TEXT PRIMARY KEY,
  app_id TEXT NOT NULL,
  status TEXT NOT NULL,
  failure_reason TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE app_secrets (
  app_id TEXT NOT NULL,
  name TEXT NOT NULL,
  ciphertext TEXT NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  PRIMARY KEY (app_id, name)
);

CREATE TABLE routes (
  id TEXT PRIMARY KEY,
  app_id TEXT NOT NULL,
  domain TEXT NOT NULL UNIQUE,
  target_port INTEGER NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

-- +goose Down
DROP TABLE routes;
DROP TABLE app_secrets;
DROP TABLE deployments;
DROP TABLE apps;
DROP TABLE agent_tokens;
DROP TABLE node_join_tokens;
DROP TABLE nodes;
DROP TABLE admin_tokens;
