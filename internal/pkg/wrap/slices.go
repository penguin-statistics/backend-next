package wrap

func Flattened[T []any](src []T) T {
	res := make(T, 0, len(src))
	for _, v := range src {
		res = append(res, v...)
	}
	return res
}

func FlattenedPtrs[T []*any](src []T) T {
	res := make(T, 0, len(src))
	for _, v := range src {
		res = append(res, v...)
	}
	return res
}
