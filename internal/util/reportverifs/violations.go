package reportverifs

type Violations map[int]*Violation

func (v Violations) Reliability(index int) int {
	if violation, ok := v[index]; ok {
		return violation.Reliability
	}

	return 0
}

type Violation struct {
	Rejection
	Name string `json:"name"`
}

type Rejection struct {
	Reliability int    `json:"reliability"`
	Message     string `json:"message"`
}
