package util

// Group holds the items of GroupBy that share Key, in their input order.
type Group[K comparable, T any] struct {
	Key   K
	Items []T
}

// GroupBy groups items by the key that key returns for them. The groups are in the order in which their keys first
// appear in items, the items of a group keep their input order, and there is no group (nil) for no items.
func GroupBy[T any, K comparable](items []T, key func(T) K) []Group[K, T] {
	var groups []Group[K, T]
	var index map[K]int
	for _, item := range items {
		k := key(item)
		i, ok := index[k]
		if !ok {
			if index == nil {
				index = make(map[K]int)
			}
			i = len(groups)
			index[k] = i
			groups = append(groups, Group[K, T]{Key: k})
		}
		groups[i].Items = append(groups[i].Items, item)
	}
	return groups
}
