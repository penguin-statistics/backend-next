package util

import (
	"reflect"
	"testing"
)

func TestGroupBy(t *testing.T) {
	type item struct{ key, id int }
	key := func(i item) int { return i.key }

	if groups := GroupBy(nil, key); groups != nil {
		t.Errorf("groups of nil: %v, want nil", groups)
	}
	if groups := GroupBy([]item{}, key); groups != nil {
		t.Errorf("groups of none: %v, want nil", groups)
	}

	items := []item{{3, 0}, {1, 1}, {3, 2}, {2, 3}, {1, 4}, {3, 5}}
	want := []Group[int, item]{
		{Key: 3, Items: []item{{3, 0}, {3, 2}, {3, 5}}},
		{Key: 1, Items: []item{{1, 1}, {1, 4}}},
		{Key: 2, Items: []item{{2, 3}}},
	}
	groups := GroupBy(items, key)
	if !reflect.DeepEqual(groups, want) {
		t.Errorf("groups %v, want %v", groups, want)
	}

	// the items of a group are a slice of their own
	groups[0].Items[0] = item{9, 9}
	if items[0] != (item{3, 0}) {
		t.Error("the input was modified through a group")
	}
}
