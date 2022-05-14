package wrap

type Tuple[K comparable, V any] struct {
	Key K
	Val V
}

func TuplesFromMap[K comparable, V any](m map[K]V) []Tuple[K, V] {
	var res []Tuple[K, V]
	for k, v := range m {
		res = append(res, Tuple[K, V]{k, v})
	}
	return res
}

func TuplePtrsFromMap[K comparable, V any](m map[K]V) []*Tuple[K, V] {
	var res []*Tuple[K, V]
	for k, v := range m {
		res = append(res, &Tuple[K, V]{k, v})
	}
	return res
}
