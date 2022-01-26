package errors

import "testing"

func TestImmutable(t *testing.T) {
	e := New(400, "INVALID_REQUEST", "invalid request: some or all request parameters are invalid")
	changedE := e.WithMessage("%s", "changed")
	if e.Message == "changed" {
		t.Errorf("Expected immutable error with message not equal to 'changed', got '%s'", e.Message)
	}
	if changedE.Message != "changed" {
		t.Errorf("Expected immutable error with message equal to 'changed', got '%s'", changedE.Message)
	}
}
