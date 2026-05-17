package api

import (
	"errors"

	"github.com/0xivanov/self-hosted-deployer/internal/repository"
)

var ErrNotFound = repository.ErrNotFound

type Code string

const (
	CodeInvalidArgument  Code = "InvalidArgument"
	CodeNotFound         Code = "NotFound"
	CodeUnauthenticated  Code = "Unauthenticated"
	CodePermissionDenied Code = "PermissionDenied"
	CodeInternal         Code = "Internal"
)

type Error struct {
	Code    Code
	Message string
}

func (e Error) Error() string {
	return string(e.Code) + ": " + e.Message
}

func InvalidArgument(message string) error {
	return Error{Code: CodeInvalidArgument, Message: message}
}

func NotFound(message string) error {
	return Error{Code: CodeNotFound, Message: message}
}

func Unauthenticated(message string) error {
	return Error{Code: CodeUnauthenticated, Message: message}
}

func PermissionDenied(message string) error {
	return Error{Code: CodePermissionDenied, Message: message}
}

func FromRepositoryError(err error, resource string) error {
	if errors.Is(err, repository.ErrNotFound) {
		return NotFound(resource + " not found")
	}
	return Error{Code: CodeInternal, Message: "internal server error"}
}

func CodeOf(err error) Code {
	var apiErr Error
	if errors.As(err, &apiErr) {
		return apiErr.Code
	}
	return CodeInternal
}
