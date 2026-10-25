package model

import (
	"fmt"
	"math/rand"
	"testing"

	"gopkg.in/guregu/null.v3"
)

func randomNullInt(r *rand.Rand) null.Int {
	if r.Intn(3) == 0 {
		return null.Int{}
	}
	return null.IntFrom(int64(r.Intn(100)))
}

// randomGlobalDropMatrixAggregate returns an aggregate with any of its parts invalid, and buckets nil, empty or not.
func randomGlobalDropMatrixAggregate(r *rand.Rand) *GlobalDropMatrixAggregate {
	a := &GlobalDropMatrixAggregate{Times: randomNullInt(r), Quantity: randomNullInt(r)}
	switch r.Intn(3) {
	case 0:
	case 1:
		a.QuantityBuckets = map[int]int{}
	default:
		a.QuantityBuckets = make(map[int]int)
		for i := r.Intn(4); i >= 0; i-- {
			a.QuantityBuckets[r.Intn(5)] = r.Intn(50)
		}
	}
	return a
}

func cloneGlobalDropMatrixAggregate(a *GlobalDropMatrixAggregate) *GlobalDropMatrixAggregate {
	c := *a
	if a.QuantityBuckets != nil {
		c.QuantityBuckets = make(map[int]int, len(a.QuantityBuckets))
		for k, v := range a.QuantityBuckets {
			c.QuantityBuckets[k] = v
		}
	}
	return &c
}

// dropAggregateString is a canonical form of a: an invalid part reads null whatever its value, and nil buckets are
// empty ones.
func dropAggregateString(a *GlobalDropMatrixAggregate) string {
	part := func(n null.Int) string {
		if !n.Valid {
			return "null"
		}
		return fmt.Sprint(n.Int64)
	}
	buckets := make(map[int]int, len(a.QuantityBuckets))
	for k, v := range a.QuantityBuckets {
		buckets[k] = v
	}
	// fmt prints maps sorted by key
	return fmt.Sprintf("times=%s quantity=%s buckets=%v", part(a.Times), part(a.Quantity), buckets)
}

func dropSum(as ...*GlobalDropMatrixAggregate) *GlobalDropMatrixAggregate {
	sum := cloneGlobalDropMatrixAggregate(as[0])
	for _, a := range as[1:] {
		sum.Add(a)
	}
	return sum
}

func TestGlobalDropMatrixAggregateAdd(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	for i := 0; i < 2000; i++ {
		a, b, c := randomGlobalDropMatrixAggregate(r), randomGlobalDropMatrixAggregate(r), randomGlobalDropMatrixAggregate(r)
		before := dropAggregateString(b)

		// identity: nothing on one side
		for _, zero := range []*GlobalDropMatrixAggregate{{}, {QuantityBuckets: map[int]int{}}} {
			if got, want := dropAggregateString(dropSum(a, zero)), dropAggregateString(a); got != want {
				t.Fatalf("a + 0 = %s, want %s", got, want)
			}
			if got, want := dropAggregateString(dropSum(zero, a)), dropAggregateString(a); got != want {
				t.Fatalf("0 + a = %s, want %s", got, want)
			}
		}
		if got, want := dropAggregateString(dropSum(a, b)), dropAggregateString(dropSum(b, a)); got != want {
			t.Fatalf("a + b = %s, b + a = %s", got, want)
		}
		if got, want := dropAggregateString(dropSum(dropSum(a, b), c)), dropAggregateString(dropSum(a, dropSum(b, c))); got != want {
			t.Fatalf("(a + b) + c = %s, a + (b + c) = %s", got, want)
		}
		if got := dropAggregateString(b); got != before {
			t.Fatalf("Add changed its argument from %s to %s", before, got)
		}

		// each part: valid if valid on either side, with the sum of the valid sides
		sum := dropSum(a, b)
		for _, part := range []struct {
			name      string
			a, b, sum null.Int
		}{{"times", a.Times, b.Times, sum.Times}, {"quantity", a.Quantity, b.Quantity, sum.Quantity}} {
			want := null.Int{}
			if part.a.Valid || part.b.Valid {
				want = null.IntFrom(part.a.ValueOrZero() + part.b.ValueOrZero())
			}
			if part.sum != want {
				t.Fatalf("%s: %v + %v = %v, want %v", part.name, part.a, part.b, part.sum, want)
			}
		}
		for k := 0; k < 5; k++ {
			if got, want := sum.QuantityBuckets[k], a.QuantityBuckets[k]+b.QuantityBuckets[k]; got != want {
				t.Fatalf("bucket %d: %d, want %d", k, got, want)
			}
		}
	}
}

func TestGlobalDropMatrixAggregateAddNulls(t *testing.T) {
	// an invalid part counts as nothing, whatever value it holds
	a := &GlobalDropMatrixAggregate{Times: null.NewInt(7, false), Quantity: null.NewInt(9, false)}
	a.Add(&GlobalDropMatrixAggregate{Times: null.IntFrom(3)})
	if a.Times != null.IntFrom(3) || a.Quantity.Valid || a.QuantityBuckets != nil {
		t.Fatalf("null + {times 3} = %s, want times 3 only", dropAggregateString(a))
	}
	a.Add(&GlobalDropMatrixAggregate{Quantity: null.IntFrom(0), QuantityBuckets: map[int]int{2: 1}})
	if a.Times != null.IntFrom(3) || a.Quantity != null.IntFrom(0) || len(a.QuantityBuckets) != 1 || a.QuantityBuckets[2] != 1 {
		t.Fatalf("got %s, want times 3, a valid quantity 0 and bucket 2 = 1", dropAggregateString(a))
	}
	a.Add(&GlobalDropMatrixAggregate{Times: null.NewInt(5, false)})
	if a.Times != null.IntFrom(3) {
		t.Fatalf("an invalid times added: %s", dropAggregateString(a))
	}
}

// randomGlobalPatternMatrixAggregates returns aggregates over stages 1 to 4 and patterns 1 to 4, maps nil or not.
func randomGlobalPatternMatrixAggregates(r *rand.Rand) *GlobalPatternMatrixAggregates {
	a := &GlobalPatternMatrixAggregates{}
	if r.Intn(4) > 0 {
		a.TimesByStageID = make(map[int]int)
		for stageId := 1; stageId <= 4; stageId++ {
			if r.Intn(2) == 0 {
				a.TimesByStageID[stageId] = r.Intn(100)
			}
		}
	}
	if r.Intn(4) > 0 {
		a.QuantitiesByStageID = make(map[int]map[int]int)
		for stageId := 1; stageId <= 4; stageId++ {
			if r.Intn(2) == 0 {
				continue
			}
			a.QuantitiesByStageID[stageId] = make(map[int]int)
			for patternId := 1; patternId <= 4; patternId++ {
				if r.Intn(2) == 0 {
					a.QuantitiesByStageID[stageId][patternId] = r.Intn(50)
				}
			}
		}
	}
	return a
}

func cloneGlobalPatternMatrixAggregates(a *GlobalPatternMatrixAggregates) *GlobalPatternMatrixAggregates {
	c := &GlobalPatternMatrixAggregates{}
	if a.TimesByStageID != nil {
		c.TimesByStageID = make(map[int]int, len(a.TimesByStageID))
		for k, v := range a.TimesByStageID {
			c.TimesByStageID[k] = v
		}
	}
	if a.QuantitiesByStageID != nil {
		c.QuantitiesByStageID = make(map[int]map[int]int, len(a.QuantitiesByStageID))
		for stageId, quantities := range a.QuantitiesByStageID {
			c.QuantitiesByStageID[stageId] = make(map[int]int, len(quantities))
			for k, v := range quantities {
				c.QuantitiesByStageID[stageId][k] = v
			}
		}
	}
	return c
}

// patternAggregatesString is a canonical form of a: nil maps are empty ones, and a stage without quantities has none.
func patternAggregatesString(a *GlobalPatternMatrixAggregates) string {
	times := make(map[int]int, len(a.TimesByStageID))
	for k, v := range a.TimesByStageID {
		times[k] = v
	}
	quantities := make(map[int]map[int]int)
	for stageId, q := range a.QuantitiesByStageID {
		if len(q) > 0 {
			quantities[stageId] = q
		}
	}
	return fmt.Sprintf("times=%v quantities=%v", times, quantities)
}

func patternSum(as ...*GlobalPatternMatrixAggregates) *GlobalPatternMatrixAggregates {
	sum := cloneGlobalPatternMatrixAggregates(as[0])
	for _, a := range as[1:] {
		sum.Add(a)
	}
	return sum
}

func TestGlobalPatternMatrixAggregatesAdd(t *testing.T) {
	r := rand.New(rand.NewSource(2))
	for i := 0; i < 2000; i++ {
		a, b, c := randomGlobalPatternMatrixAggregates(r), randomGlobalPatternMatrixAggregates(r), randomGlobalPatternMatrixAggregates(r)
		before := patternAggregatesString(b)

		for _, zero := range []*GlobalPatternMatrixAggregates{{}, {TimesByStageID: map[int]int{}, QuantitiesByStageID: map[int]map[int]int{}}} {
			if got, want := patternAggregatesString(patternSum(a, zero)), patternAggregatesString(a); got != want {
				t.Fatalf("a + 0 = %s, want %s", got, want)
			}
			if got, want := patternAggregatesString(patternSum(zero, a)), patternAggregatesString(a); got != want {
				t.Fatalf("0 + a = %s, want %s", got, want)
			}
		}
		if got, want := patternAggregatesString(patternSum(a, b)), patternAggregatesString(patternSum(b, a)); got != want {
			t.Fatalf("a + b = %s, b + a = %s", got, want)
		}
		if got, want := patternAggregatesString(patternSum(patternSum(a, b), c)), patternAggregatesString(patternSum(a, patternSum(b, c))); got != want {
			t.Fatalf("(a + b) + c = %s, a + (b + c) = %s", got, want)
		}
		if got := patternAggregatesString(b); got != before {
			t.Fatalf("Add changed its argument from %s to %s", before, got)
		}

		// the times of a stage: present if present on either side (also as 0), with the sum
		sum := patternSum(a, b)
		for stageId := 1; stageId <= 4; stageId++ {
			at, aok := a.TimesByStageID[stageId]
			bt, bok := b.TimesByStageID[stageId]
			st, sok := sum.TimesByStageID[stageId]
			if sok != (aok || bok) || st != at+bt {
				t.Fatalf("times of stage %d: %d (%v) + %d (%v) = %d (%v)", stageId, at, aok, bt, bok, st, sok)
			}
			for patternId := 1; patternId <= 4; patternId++ {
				if got, want := sum.QuantitiesByStageID[stageId][patternId],
					a.QuantitiesByStageID[stageId][patternId]+b.QuantitiesByStageID[stageId][patternId]; got != want {
					t.Fatalf("quantity of stage %d pattern %d: %d, want %d", stageId, patternId, got, want)
				}
			}
		}
	}
}
