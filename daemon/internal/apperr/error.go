package apperr

import (
	"errors"
	"fmt"
)

type Error struct {
	Code      string   `json:"code"`
	Message   string   `json:"message"`
	Stage     string   `json:"stage,omitempty"`
	Details   []string `json:"details,omitempty"`
	Retryable bool     `json:"retryable,omitempty"`
	Cause     error    `json:"-"`
}

func (e *Error) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.Cause)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func (e *Error) Unwrap() error { return e.Cause }

func New(code, message string) *Error {
	return &Error{Code: code, Message: message}
}

func Wrap(code, message string, cause error) *Error {
	return &Error{Code: code, Message: message, Cause: cause}
}

func Describe(err error, stage string, retryable bool, details ...string) *Error {
	source := From(err)
	if source == nil {
		return nil
	}
	result := *source
	result.Details = append([]string(nil), source.Details...)
	if stage != "" {
		result.Stage = stage
	}
	result.Retryable = result.Retryable || retryable
	result.Details = append(result.Details, details...)
	if source.Cause != nil {
		result.Details = append(result.Details, source.Cause.Error())
	}
	return &result
}

func From(err error) *Error {
	if err == nil {
		return nil
	}
	var target *Error
	if errors.As(err, &target) {
		return target
	}
	return Wrap("INTERNAL", "守护进程内部错误", err)
}
