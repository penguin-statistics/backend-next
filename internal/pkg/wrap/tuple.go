package wrap

type Tuple[K comparable, V any] struct {
	Key K
	Val V
}

func TuplesFromMap[K comparable, V any](m map[K]V) []Tuple[K, V] {
	res := make([]Tuple[K, V], 0, len(m))
	for k, v := range m {
		res = append(res, Tuple[K, V]{k, v})
	}
	return res
}

func TuplePtrsFromMap[K comparable, V any](m map[K]V) []*Tuple[K, V] {
	res := make([]*Tuple[K, V], 0, len(m))
	for k, v := range m {
		res = append(res, &Tuple[K, V]{k, v})
	}
	return res
}
