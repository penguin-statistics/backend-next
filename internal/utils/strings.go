package utils

import (
	"regexp"
	"strconv"
)

var validIdRegex = regexp.MustCompile(`^[a-zA-Z0-9\-_]+$`)

func IsAscii(s string) bool {
	for _, r := range s {
		if r > 127 {
			return false
		}
	}
	return true
}

func IsValidId(s string) bool {
	if len(s) > 32 {
		return false
	}

	return validIdRegex.MatchString(s)
}

func IsInt(s string) bool {
	_, err := strconv.ParseInt(s, 10, 64)
	return err == nil
}

func NonNullString(s string) bool {
	return s != ""
}
