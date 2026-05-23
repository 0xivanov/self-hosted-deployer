package db

import (
	"database/sql"
	"errors"
	"time"
)

var ErrNotFound = errors.New("not found")

func mapSQLError(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

func mapRowsAffected(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

func formatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

func formatOptionalTime(t *time.Time) sql.NullString {
	if t == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: formatTime(*t), Valid: true}
}

func parseStoredTime(value string) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, value)
	return t
}

func parseOptionalStoredTime(value sql.NullString) *time.Time {
	if !value.Valid {
		return nil
	}
	t := parseStoredTime(value.String)
	return &t
}

func optionalString(value string) sql.NullString {
	return sql.NullString{String: value, Valid: value != ""}
}

func parseOptionalString(value sql.NullString) string {
	if !value.Valid {
		return ""
	}
	return value.String
}
