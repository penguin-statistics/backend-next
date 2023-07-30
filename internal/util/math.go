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

func CalcStdDevFromQuantityBuckets(quantityBuckets map[int]int, times int, isUnbiased bool) float64 {
	sum := 0
	squareSum := 0
	for quantity, times := range quantityBuckets {
		sum += quantity * times
		squareSum += quantity * quantity * times
	}
	denominator := times
	if isUnbiased {
		denominator -= 1
	}
	variance := float64(squareSum)/float64(denominator) - math.Pow(float64(sum)/float64(times), 2)*float64(times)/float64(denominator)
	if variance < -0.05 {
		// should not happen, unless something is wrong with the drop pattern
		log.Error().Msgf("variance is less than -0.05: %f", variance)
		return 0
	} else if variance < 0 {
		// maybe the float calculation is not accurate enough, we don't log error when -0.05 <= variance < 0
		log.Warn().Msgf("variance is less than 0: %f", variance)
		return 0
	}
	return math.Sqrt(variance)
}

func CombineTwoBundles(bundle1, bundle2 *StatsBundle) *StatsBundle {
	n := bundle1.N + bundle2.N
	avg := (bundle1.Avg*float64(bundle1.N) + bundle2.Avg*float64(bundle2.N)) / float64(n)
	squareAvg := (bundle1.calcSquareAvg()*float64(bundle1.N) + bundle2.calcSquareAvg()*float64(bundle2.N)) / float64(n)
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

func CalcPooledStdDev(bundle1, bundle2 *StatsBundle) float64 {
	square := float64(float64(bundle1.N-1)*math.Pow(bundle1.StdDev, 2)+float64(bundle2.N-1)*math.Pow(bundle2.StdDev, 2)) / float64(bundle1.N+bundle2.N-2)
	if square < 0 {
		// should not happen
		log.Error().Msgf("square %f is less than 0 in CalcPooledStdDev", square)
		return 0
	}
	return math.Sqrt(square)
}

func CalcTScore(bundle1, bundle2 *StatsBundle) float64 {
	SP := CalcPooledStdDev(bundle1, bundle2)
	if SP == 0 {
		return 0
	}
	SE := SP * math.Sqrt(1.0/float64(bundle1.N)+1.0/float64(bundle2.N))
	return math.Abs((bundle1.Avg - bundle2.Avg) / SE)
}

func RoundFloat64(f float64, n int) float64 {
	pow := math.Pow10(n)
	return math.Round(f*pow) / pow
}

func (bundle *StatsBundle) calcSquareAvg() float64 {
	return math.Pow(bundle.Avg, 2) + math.Pow(bundle.StdDev, 2)
}
