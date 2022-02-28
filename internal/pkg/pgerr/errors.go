package pgerr

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

	// ErrInvalidReq is returned when a request is invalid.
	ErrInvalidReq = New(fiber.StatusBadRequest, CodeInvalidRequest, "invalid request: some or all request parameters are invalid")

	// ErrInternalError is returned when an internal error occurs.
	ErrInternalError = New(fiber.StatusInternalServerError, CodeInternalError, "internal server error occurred")
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

func (e PenguinError) Msg(format string, parts ...interface{}) *PenguinError {
	e.Message = fmt.Sprintf(format, parts...)
	return &e
}

func (e PenguinError) WithExtras(extras Extras) *PenguinError {
	e.Extras = &extras
	return &e
}

func NewInvalidViolations(violations interface{}) *PenguinError {
	// copy ErrInvalidRequest as e
	e := *ErrInvalidReq
	e.Extras = &Extras{
		"violations": violations,
	}
	return &e
}

func (e *PenguinError) Error() string {
	return fmt.Sprintf("%s: %s", e.ErrorCode, e.Message)
}
