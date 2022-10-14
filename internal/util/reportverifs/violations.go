package reportverifs

import "bytes"

type Violations map[int]*Violation

func (v Violations) Reliability(index int) int {
	if violation, ok := v[index]; ok {
		return violation.Reliability
	}

	return 0
}

func (v Violations) String() string {
	var buf bytes.Buffer

	for i, violation := range v {
		buf.WriteString(violation.Message)
		if i != len(v)-1 {
			buf.WriteString("; ")
		}
	}

	return buf.String()
}

type Violation struct {
	Rejection
	Name string `json:"name"`
}

type Rejection struct {
	Reliability int    `json:"reliability"`
	Message     string `json:"message"`
}
