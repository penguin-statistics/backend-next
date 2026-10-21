package observability

import "testing"

func TestHTTPStatusClass(t *testing.T) {
	tests := []struct {
		code int
		want string
	}{
		{0, "other"},
		{100, "1xx"},
		{200, "2xx"},
		{304, "3xx"},
		{404, "4xx"},
		{499, "4xx"},
		{500, "5xx"},
		{599, "5xx"},
		{600, "other"},
	}
	for _, tt := range tests {
		if got := HTTPStatusClass(tt.code); got != tt.want {
			t.Errorf("HTTPStatusClass(%d) = %q, want %q", tt.code, got, tt.want)
		}
	}
}
