package errors

import (
	"fmt"

	"github.com/gofiber/fiber/v2"
)

type PenguinError struct {
	StatusCode int    `example:"400"`
	ErrorCode  string `example:"INVALID_REQUEST"`
	Message    string `example:"invalid request: request parameters are invalid"`
}

func New(statusCode int, errorCode string, message string) *PenguinError {
	return &PenguinError{
		StatusCode: statusCode,
		ErrorCode:  errorCode,
		Message:    message,
	}
}

func (e *PenguinError) Error() string {
	return fmt.Sprintf("%s: %s", e.ErrorCode, e.Message)
}

const (
	CodeNotFound       = "NOT_FOUND"
	CodeInvalidRequest = "INVALID_REQUEST"
)

var (
	// ErrNotFound is returned when a resource is not found.
	ErrNotFound = New(fiber.StatusBadRequest, CodeNotFound, "resource not found with given parameters")

	// ErrInvalidRequest is returned when a request is invalid.
	ErrInvalidRequest = New(fiber.StatusBadRequest, CodeInvalidRequest, "invalid request")
)
