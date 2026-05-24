-- +goose Up
ALTER TABLE nodes ADD COLUMN wireguard_ip TEXT NOT NULL DEFAULT '';
ALTER TABLE nodes ADD COLUMN wireguard_public_key TEXT NOT NULL DEFAULT '';

CREATE UNIQUE INDEX nodes_wireguard_ip_unique
  ON nodes (wireguard_ip)
  WHERE wireguard_ip <> '';

CREATE UNIQUE INDEX nodes_wireguard_public_key_unique
  ON nodes (wireguard_public_key)
  WHERE wireguard_public_key <> '';

-- +goose Down
DROP INDEX nodes_wireguard_public_key_unique;
DROP INDEX nodes_wireguard_ip_unique;
ALTER TABLE nodes DROP COLUMN wireguard_public_key;
ALTER TABLE nodes DROP COLUMN wireguard_ip;
