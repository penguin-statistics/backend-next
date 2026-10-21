package cache

import (
	"testing"
	"time"
)

func TestSingularGet(t *testing.T) {
	c := NewSingular[testValue]("test")
	var v testValue
	if err := c.Get(&v); err != ErrNotFound {
		t.Errorf("got %v before a set, want ErrNotFound", err)
	}

	c.Set(testValue{ID: 1, Name: "one"}, time.Minute)
	if err := c.Get(&v); err != nil || v.ID != 1 || v.Name != "one" {
		t.Errorf("got %+v (%v), want the cached value", v, err)
	}

	if err := c.Delete(); err != nil {
		t.Fatal(err)
	}
	if err := c.Get(&v); err != ErrNotFound {
		t.Errorf("got %v after a delete, want ErrNotFound", err)
	}
}

func TestSingularGetKeepsNilMaps(t *testing.T) {
	c := NewSingular[map[string]int]("test")
	c.Set(nil, time.Minute)
	m := map[string]int{"a": 1}
	if err := c.Get(&m); err != nil || m != nil {
		t.Errorf("got %v (%v), want a nil map", m, err)
	}
}

func TestSingularMutexGetSetCalculatesOnAMiss(t *testing.T) {
	c := NewSingular[[]int]("test")
	calculations := 0
	valueFunc := func() ([]int, error) {
		calculations++
		return []int{1, 2}, nil
	}

	for i := 0; i < 2; i++ {
		var v []int
		if err := c.MutexGetSet(&v, valueFunc, time.Minute); err != nil || len(v) != 2 || v[1] != 2 {
			t.Errorf("got %v (%v), want [1 2]", v, err)
		}
	}
	if calculations != 1 {
		t.Errorf("calculated %d times, want once", calculations)
	}
}

func TestSingularGetOfAPointerTypeKeepsThePointer(t *testing.T) {
	c := NewSingular[*testValue]("test")
	calculated := &testValue{ID: 2}
	var v *testValue
	if err := c.MutexGetSet(&v, func() (*testValue, error) { return calculated, nil }, time.Minute); err != nil || v != calculated {
		t.Errorf("got %p (%v), want the calculated pointer %p", v, err, calculated)
	}

	cached := &testValue{ID: 1}
	c.Set(cached, time.Minute)
	if err := c.Get(&v); err != nil || v != cached {
		t.Errorf("got %p (%v), want the cached pointer %p", v, err, cached)
	}
}

func BenchmarkSingularGet(b *testing.B) {
	c := NewSingular[testValue]("test")
	c.Set(testValue{ID: 1, Name: "one", Tags: []string{"a"}}, time.Minute)
	var v testValue
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := c.Get(&v); err != nil {
			b.Fatal(err)
		}
	}
}
