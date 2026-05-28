-- +goose Up
ALTER TABLE nodes ADD COLUMN vpn_status TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE nodes DROP COLUMN vpn_status;
