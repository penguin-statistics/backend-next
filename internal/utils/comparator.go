package utils

import (
	"strconv"
	"strings"
)

func CompareStageCode(code1, code2 string) bool {
	splittedCode1 := strings.Split(code1, "-")
	splittedCode2 := strings.Split(code2, "-")
	if len(splittedCode1) != len(splittedCode2) {
		return len(splittedCode1) < len(splittedCode2)
	}

	for i := 0; i < len(splittedCode1); i++ {
		if splittedCode1[i] == splittedCode2[i] {
			continue
		}
		return subStageCodeComparator(splittedCode1[i], splittedCode2[i])
	}
	return false
}

func subStageCodeComparator(part1, part2 string) bool {
	num1, err1 := strconv.Atoi(part1)
	num2, err2 := strconv.Atoi(part2)
	if err1 == nil && err2 == nil {
		return num1 < num2
	}

	compareResult := strings.Compare(part1, part2)
	if compareResult == -1 {
		return true
	} else if compareResult == 1 {
		return false
	}
	return true
}
