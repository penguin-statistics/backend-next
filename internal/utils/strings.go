package utils

import (
	"regexp"
	"strconv"
	"strings"
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

// AddSpace Adds a space, if not present, between ASCII Characters and Non-ASCII Characters.
// Notice that Non-ASCII characters could be multi-byte unicode sequence.
// For example, "中文english" -> "中文 english"
func AddSpace(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if i > 0 && IsAscii(s[i:i+1]) != IsAscii(s[i-1:i]) && s[i-1] != ' ' && s[i] != ' ' {
			b.WriteByte(' ')
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
