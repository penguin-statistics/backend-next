package util

import (
	"math"

	"github.com/rs/zerolog/log"
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
	variance := float64(squareSum)/float64(times) - math.Pow(float64(sum)/float64(times), 2)
	if variance < 0 {
		// should not happen, unless something is wrong with the drop pattern
		log.Error().Msgf("variance is less than 0: %f", variance)
		return 0
	}
	return math.Sqrt(variance)
}

func CombineTwoBundles(bundle1, bundle2 *StatsBundle) *StatsBundle {
	n := bundle1.N + bundle2.N
	avg := (bundle1.Avg*float64(bundle1.N) + bundle2.Avg*float64(bundle2.N)) / float64(n)
	squareAvg := (calcSquareAvg(bundle1)*float64(bundle1.N) + calcSquareAvg(bundle2)*float64(bundle2.N)) / float64(n)
	variance := squareAvg - math.Pow(avg, 2)
	stdDev := 0.0
	if variance < 0 {
		// should not happen, unless something is wrong with the drop pattern
		log.Error().Msgf("variance is less than 0: %f", variance)
	} else {
		stdDev = math.Sqrt(variance)
	}
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
