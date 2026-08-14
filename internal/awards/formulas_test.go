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
		if formula.Version != 3 || formula.Status != "active" {
			t.Errorf("formula %q is not active v3: %#v", code, formula)
		}
	}
}

func TestFinalFormulaWeights(t *testing.T) {
	catalog, err := LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	expected := map[string]map[string]float64{
		PlayerOfSeason:           {"reliable_base": .85, "season_volume": .15},
		DefenderOfSeason:         {"reliable_base": .35, "reliable_defence": .50, "season_volume": .15},
		GoalkeeperOfSeason:       {"reliable_base": .25, "reliable_shot_stopping": .35, "distribution": .15, "cross_control": .075, "sweeping": .075, "error_avoidance": .05, "season_volume": .05},
		YoungPlayerOfSeason:      {"reliable_base": .85, "season_volume": .15},
		ImpactSubstituteOfSeason: {"reliable_impact": .55, "reliable_substitute_base": .25, "substitute_volume": .20},
		MostImprovedPlayer:       {"base_growth": .45, "efficiency_growth": .20, "influence_growth": .20, "role_growth": .15},
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
	if catalog.CommonMetrics["provider_rating_policy"] != "provider-published player ratings are never inputs" {
		t.Fatalf("catalog must explicitly exclude provider ratings: %#v", catalog.CommonMetrics)
	}
}

func TestPlayerRawScoreUsesFinalReliabilityAndVolumeWeights(t *testing.T) {
	catalog, err := LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	formula, _ := catalog.Formula(PlayerOfSeason)

	raw, err := formula.RawScore(map[string]float64{
		"reliable_base": 1.5,
		"season_volume": 0.5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(raw-1.35) > 1e-12 {
		t.Fatalf("raw score = %f, want 1.35", raw)
	}
}

func TestDisplayScoreIsMonotonicLogisticScale(t *testing.T) {
	catalog, err := LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	formula, _ := catalog.Formula(MostImprovedPlayer)
	neutral := map[string]float64{
		"base_growth":       0,
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
	neutral["base_growth"] = 1
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
		"reliable_base": math.NaN(),
		"season_volume": 0.5,
	}
	if _, err := formula.Score(components); err == nil {
		t.Fatal("expected finite component error")
	}
}
