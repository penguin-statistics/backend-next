package errors

import (
	"fmt"

	"github.com/gofiber/fiber/v2"
)

const (
	CodeNotFound       = "NOT_FOUND"
	CodeInvalidRequest = "INVALID_REQUEST"
	CodeInternalError  = "INTERNAL_ERROR"
)

var (
	// ErrNotFound is returned when a resource is not found.
	ErrNotFound = New(fiber.StatusBadRequest, CodeNotFound, "resource not found with given parameters")

	// ErrInvalidRequest is returned when a request is invalid.
	ErrInvalidRequest = New(fiber.StatusBadRequest, CodeInvalidRequest, "invalid request: some or all request parameters are invalid")
)

type Extras map[string]interface{}

type PenguinError struct {
	StatusCode int    `example:"400"`
	ErrorCode  string `example:"INVALID_REQUEST"`
	Message    string `example:"invalid request: some or all request parameters are invalid"`
	Extras     *Extras
}

func New(statusCode int, errorCode string, message string) *PenguinError {
	return &PenguinError{
		StatusCode: statusCode,
		ErrorCode:  errorCode,
		Message:    message,
	}
}

func (e PenguinError) WithMessage(format string, parts ...interface{}) *PenguinError {
	e.Message = fmt.Sprintf(format, parts...)
	return &e
}

func (e PenguinError) WithExtras(extras Extras) *PenguinError {
	e.Extras = &extras
	return &e
}

func NewInvalidViolations(violations interface{}) *PenguinError {
	e := ErrInvalidRequest
	e.Extras = &Extras{
		"violations": violations,
	}
	return e
}

func (e *PenguinError) Error() string {
	return fmt.Sprintf("%s: %s", e.ErrorCode, e.Message)
}
