// Package performance implements the provider-independent base player rating.
//
// Match statistics are converted to per-90 rates (or attempt-adjusted rates),
// standardized inside each edition/position cohort, and combined with
// position-specific weights. No provider-published rating is an input.
package performance

import (
	"fmt"
	"math"
	"sort"
)

const (
	// ReliabilityPriorMinutes is the amount of playing time at which observed
	// performance and the reliability prior have equal weight.
	ReliabilityPriorMinutes = 900.0

	// QualityWeight and VolumeWeight form the general leaderboard/Player of the
	// Season formula after the positional quality has been reliability-adjusted.
	QualityWeight = 0.85
	VolumeWeight  = 0.15
)

// Dimensions are standardized scores, not raw counts. The normalization cohort
// is all players in the same edition, resolved position, and calculation window.
type Dimensions struct {
	Attack      float64
	Progression float64
	Defence     float64
	Control     float64
	Influence   float64
}

// Weights defines how the five dimensions contribute for one position.
type Weights struct {
	Attack      float64
	Progression float64
	Defence     float64
	Control     float64
	Influence   float64
}

// PositionWeights is the immutable v3 positional policy. Each row totals one.
var PositionWeights = map[string]Weights{
	"GK":  {Attack: 0.05, Progression: 0.10, Defence: 0.10, Control: 0.30, Influence: 0.45},
	"DEF": {Attack: 0.10, Progression: 0.20, Defence: 0.35, Control: 0.20, Influence: 0.15},
	"MID": {Attack: 0.15, Progression: 0.30, Defence: 0.15, Control: 0.20, Influence: 0.20},
	"FWD": {Attack: 0.35, Progression: 0.20, Defence: 0.05, Control: 0.15, Influence: 0.25},
}

// Result contains the reproducible stages of the base rating calculation.
type Result struct {
	Quality         float64
	Reliability     float64
	ReliableQuality float64
	Volume          float64
	RawScore        float64
	ScoreMilli      int64
}

// Calculate creates the general base rating from already standardized metrics.
// RawScore determines rank. ScoreMilli is only a monotonic 0..100 display scale.
func Calculate(position string, dimensions Dimensions, minutes, cohortMaxMinutes float64) (Result, error) {
	weights, ok := PositionWeights[position]
	if !ok {
		return Result{}, fmt.Errorf("unsupported position %q", position)
	}
	values := []float64{dimensions.Attack, dimensions.Progression, dimensions.Defence, dimensions.Control, dimensions.Influence, minutes, cohortMaxMinutes}
	for _, value := range values {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return Result{}, fmt.Errorf("performance inputs must be finite")
		}
	}
	if minutes < 0 || cohortMaxMinutes <= 0 || minutes > cohortMaxMinutes {
		return Result{}, fmt.Errorf("invalid minutes %.3f/%.3f", minutes, cohortMaxMinutes)
	}

	quality := weights.Attack*dimensions.Attack +
		weights.Progression*dimensions.Progression +
		weights.Defence*dimensions.Defence +
		weights.Control*dimensions.Control +
		weights.Influence*dimensions.Influence
	reliability := minutes / (minutes + ReliabilityPriorMinutes)
	volume := math.Sqrt(minutes / cohortMaxMinutes)
	raw := QualityWeight*quality*reliability + VolumeWeight*volume

	return Result{
		Quality:         quality,
		Reliability:     reliability,
		ReliableQuality: quality * reliability,
		Volume:          volume,
		RawScore:        raw,
		ScoreMilli:      PublicScoreMilli(raw),
	}, nil
}

// AdjustedRate shrinks a success rate toward its positional cohort rate before
// standardization. This prevents perfect percentages from a few attempts from
// dominating stable percentages from meaningful samples.
func AdjustedRate(successes, attempts, cohortRate, priorAttempts float64) (float64, error) {
	values := []float64{successes, attempts, cohortRate, priorAttempts}
	for _, value := range values {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return 0, fmt.Errorf("rate inputs must be finite")
		}
	}
	if successes < 0 || attempts < successes || cohortRate < 0 || cohortRate > 1 || priorAttempts < 0 || attempts+priorAttempts == 0 {
		return 0, fmt.Errorf("invalid adjusted-rate inputs")
	}
	return (successes + priorAttempts*cohortRate) / (attempts + priorAttempts), nil
}

// RobustZScores applies median/MAD standardization. It falls back to population
// Z-scores when MAD is zero and to zero when the cohort has no dispersion.
func RobustZScores(values []float64) ([]float64, error) {
	if len(values) == 0 {
		return nil, nil
	}
	for _, value := range values {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return nil, fmt.Errorf("normalization values must be finite")
		}
	}
	center := median(values)
	deviations := make([]float64, len(values))
	for index, value := range values {
		deviations[index] = math.Abs(value - center)
	}
	mad := median(deviations)
	result := make([]float64, len(values))
	if mad > 0 {
		scale := 1.4826 * mad
		for index, value := range values {
			result[index] = (value - center) / scale
		}
		return result, nil
	}

	mean := 0.0
	for _, value := range values {
		mean += value
	}
	mean /= float64(len(values))
	variance := 0.0
	for _, value := range values {
		variance += (value - mean) * (value - mean)
	}
	standardDeviation := math.Sqrt(variance / float64(len(values)))
	if standardDeviation == 0 {
		return result, nil
	}
	for index, value := range values {
		result[index] = (value - mean) / standardDeviation
	}
	return result, nil
}

// PublicScoreMilli maps an unbounded standardized score to exact display
// millipoints in [0, 100000]. The monotonic mapping cannot change rank order.
func PublicScoreMilli(raw float64) int64 {
	return int64(math.Round(100_000 / (1 + math.Exp(-raw))))
}

func median(values []float64) float64 {
	ordered := append([]float64(nil), values...)
	sort.Float64s(ordered)
	middle := len(ordered) / 2
	if len(ordered)%2 == 1 {
		return ordered[middle]
	}
	return (ordered[middle-1] + ordered[middle]) / 2
}
