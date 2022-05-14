package wrap

func Flattened[T []any](src []T) T {
	var res T
	for _, v := range src {
		res = append(res, v...)
	}
	return res
}

func FlattenedPtrs[T []*any](src []T) T {
	var res T
	for _, v := range src {
		res = append(res, v...)
	}
	return res
}
