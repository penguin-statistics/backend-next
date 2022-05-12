package util

import (
	"math"
)

type StatsBundle struct {
	N      int
	Avg    float64
	StdDev float64
}

func NewStatsBundle(n int, avg float64, stdDev float64) *StatsBundle {
	return &StatsBundle{
		N:      n,
		Avg:    avg,
		StdDev: stdDev,
	}
}

func CalcStdDevFromQuantityBuckets(quantityBuckets map[int]int, times int) float64 {
	sum := 0
	squareSum := 0
	for quantity, times := range quantityBuckets {
		sum += quantity * times
		squareSum += quantity * quantity * times
	}
	return math.Sqrt(float64(squareSum)/float64(times) - math.Pow(float64(sum)/float64(times), 2))
}

func CombineTwoBundles(bundle1, bundle2 *StatsBundle) *StatsBundle {
	n := bundle1.N + bundle2.N
	avg := (bundle1.Avg*float64(bundle1.N) + bundle2.Avg*float64(bundle2.N)) / float64(n)
	squareAvg := (calcSquareAvg(bundle1)*float64(bundle1.N) + calcSquareAvg(bundle2)*float64(bundle2.N)) / float64(n)
	stdDev := math.Sqrt(squareAvg - math.Pow(avg, 2))
	return &StatsBundle{
		N:      n,
		Avg:    avg,
		StdDev: stdDev,
	}
}

func RoundFloat64(f float64, n int) float64 {
	pow10_n := math.Pow10(n)
	return math.Round(f*pow10_n) / pow10_n
}

func calcSquareAvg(bundle *StatsBundle) float64 {
	return math.Pow(bundle.Avg, 2) + math.Pow(bundle.StdDev, 2)
}
