package performance

import (
	"math"
	"testing"
)

func TestPositionWeightsTotalOne(t *testing.T) {
	for position, weights := range PositionWeights {
		total := weights.Attack + weights.Progression + weights.Defence + weights.Control + weights.Influence
		if math.Abs(total-1) > 1e-12 {
			t.Errorf("%s weights total %f", position, total)
		}
	}
}

func TestCalculateUsesPositionWeightsAndReliability(t *testing.T) {
	dimensions := Dimensions{Attack: 2, Progression: 1, Defence: -1, Control: 0.5, Influence: 1.5}
	forward, err := Calculate("FWD", dimensions, 900, 1800)
	if err != nil {
		t.Fatal(err)
	}
	defender, err := Calculate("DEF", dimensions, 900, 1800)
	if err != nil {
		t.Fatal(err)
	}
	if forward.Quality <= defender.Quality {
		t.Fatalf("forward quality %.3f should exceed defender quality %.3f for attacking inputs", forward.Quality, defender.Quality)
	}
	if forward.Reliability != 0.5 {
		t.Fatalf("reliability = %f, want .5", forward.Reliability)
	}
	wantRaw := QualityWeight*forward.Quality*0.5 + VolumeWeight*math.Sqrt(0.5)
	if math.Abs(forward.RawScore-wantRaw) > 1e-12 {
		t.Fatalf("raw score = %f, want %f", forward.RawScore, wantRaw)
	}
}

func TestReliabilityReducesSmallSampleInflation(t *testing.T) {
	dimensions := Dimensions{Attack: 2, Progression: 2, Defence: 2, Control: 2, Influence: 2}
	short, _ := Calculate("MID", dimensions, 90, 1800)
	established, _ := Calculate("MID", dimensions, 900, 1800)
	if short.ReliableQuality >= established.ReliableQuality || short.RawScore >= established.RawScore {
		t.Fatalf("short sample %#v should rank below established sample %#v", short, established)
	}
}

func TestAdjustedRateShrinksSmallSamples(t *testing.T) {
	small, err := AdjustedRate(2, 2, 0.5, 20)
	if err != nil {
		t.Fatal(err)
	}
	large, err := AdjustedRate(40, 40, 0.5, 20)
	if err != nil {
		t.Fatal(err)
	}
	if !(small < large && small > 0.5) {
		t.Fatalf("adjusted rates small=%f large=%f", small, large)
	}
}

func TestRobustZScoresAndFallback(t *testing.T) {
	scores, err := RobustZScores([]float64{1, 2, 3, 100})
	if err != nil {
		t.Fatal(err)
	}
	if scores[3] <= scores[2] || scores[0] >= 0 || scores[2] <= 0 {
		t.Fatalf("unexpected robust scores: %#v", scores)
	}

	flat, err := RobustZScores([]float64{4, 4, 4})
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range flat {
		if value != 0 {
			t.Fatalf("flat cohort should normalize to zero: %#v", flat)
		}
	}
}

func TestPublicScoreIsMonotonic(t *testing.T) {
	if PublicScoreMilli(-1) >= PublicScoreMilli(0) || PublicScoreMilli(0) >= PublicScoreMilli(1) {
		t.Fatal("public score must preserve raw score order")
	}
	if PublicScoreMilli(0) != 50_000 {
		t.Fatalf("neutral public score = %d", PublicScoreMilli(0))
	}
}
