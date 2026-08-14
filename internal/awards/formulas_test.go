package awards

import (
	"math"
	"testing"
)

func TestPublishedCatalogContainsSixVersionedFormulas(t *testing.T) {
	catalog, err := LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}

	codes := []string{
		PlayerOfSeason,
		DefenderOfSeason,
		GoalkeeperOfSeason,
		YoungPlayerOfSeason,
		ImpactSubstituteOfSeason,
		MostImprovedPlayer,
	}
	for _, code := range codes {
		formula, ok := catalog.Formula(code)
		if !ok {
			t.Errorf("missing formula %q", code)
			continue
		}
		if formula.Version != 2 || formula.Status != "active" {
			t.Errorf("formula %q is not active v2: %#v", code, formula)
		}
	}
}

func TestFinalFormulaWeights(t *testing.T) {
	catalog, err := LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	expected := map[string]map[string]float64{
		PlayerOfSeason:           {"reliable_quality": .80, "season_volume": .20},
		DefenderOfSeason:         {"reliable_defence": .85, "season_volume": .15},
		GoalkeeperOfSeason:       {"reliable_shot_stopping": .40, "distribution": .20, "cross_control": .15, "sweeping": .10, "error_avoidance": .10, "season_volume": .05},
		YoungPlayerOfSeason:      {"reliable_quality": .80, "season_volume": .20},
		ImpactSubstituteOfSeason: {"reliable_impact": .55, "reliable_substitute_quality": .25, "substitute_volume": .20},
		MostImprovedPlayer:       {"quality_growth": .45, "efficiency_growth": .20, "influence_growth": .20, "role_growth": .15},
	}
	for code, weights := range expected {
		formula, _ := catalog.Formula(code)
		if len(formula.Components) != len(weights) {
			t.Fatalf("%s has %d components, want %d", code, len(formula.Components), len(weights))
		}
		for _, component := range formula.Components {
			if component.Weight != weights[component.Code] {
				t.Errorf("%s/%s weight = %.2f, want %.2f", code, component.Code, component.Weight, weights[component.Code])
			}
		}
	}
	progression, _ := catalog.Formula(MostImprovedPlayer)
	if progression.Eligibility["total_rounds"] != float64(22) {
		t.Fatalf("progression must use the 22-round first-half/second-half split: %#v", progression.Eligibility)
	}
}

func TestPlayerRawScoreUsesFinalReliabilityAndVolumeWeights(t *testing.T) {
	catalog, err := LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	formula, _ := catalog.Formula(PlayerOfSeason)

	raw, err := formula.RawScore(map[string]float64{
		"reliable_quality": 1.5,
		"season_volume":    0.5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(raw-1.3) > 1e-12 {
		t.Fatalf("raw score = %f, want 1.3", raw)
	}
}

func TestDisplayScoreIsMonotonicLogisticScale(t *testing.T) {
	catalog, err := LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	formula, _ := catalog.Formula(MostImprovedPlayer)
	neutral := map[string]float64{
		"quality_growth":    0,
		"efficiency_growth": 0,
		"influence_growth":  0,
		"role_growth":       0,
	}
	neutralScore, err := formula.Score(neutral)
	if err != nil {
		t.Fatal(err)
	}
	if neutralScore != 50_000 {
		t.Fatalf("neutral score = %d, want 50000", neutralScore)
	}
	neutral["quality_growth"] = 1
	improvedScore, err := formula.Score(neutral)
	if err != nil {
		t.Fatal(err)
	}
	if improvedScore <= neutralScore {
		t.Fatalf("improved score = %d, want > %d", improvedScore, neutralScore)
	}
}

func TestRawScoreHonorsLowerIsBetterComponents(t *testing.T) {
	formula := Formula{
		Aggregation: weightedStandardized,
		Components:  []Component{{Code: "errors", Weight: 1, Direction: "lower"}},
	}
	raw, err := formula.RawScore(map[string]float64{"errors": 2})
	if err != nil {
		t.Fatal(err)
	}
	if raw != -2 {
		t.Fatalf("raw score = %f, want -2", raw)
	}
}

func TestScoreRejectsMissingOrInvalidInputs(t *testing.T) {
	catalog, err := LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	formula, _ := catalog.Formula(PlayerOfSeason)
	if _, err := formula.Score(map[string]float64{}); err == nil {
		t.Fatal("expected missing component error")
	}
	components := map[string]float64{
		"reliable_quality": math.NaN(),
		"season_volume":    0.5,
	}
	if _, err := formula.Score(components); err == nil {
		t.Fatal("expected finite component error")
	}
}
