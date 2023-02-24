package test

import (
	"io"
	"net/http"
)

func bodyString(resp *http.Response) string {
	body := resp.Body
	defer body.Close()

	bodyBytes, err := io.ReadAll(body)
	if err != nil {
		return "[!] error: failed to read response body: " + err.Error()
	}

	return string(bodyBytes)
}
