package awards

import "testing"

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
		if formula.Version != 1 || formula.Status != "active" {
			t.Errorf("formula %q is not active v1: %#v", code, formula)
		}
	}
}

func TestWeightedScoreHonorsLowerIsBetterComponents(t *testing.T) {
	catalog, err := LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	formula, _ := catalog.Formula(DefenderOfSeason)

	components := make(map[string]float64, len(formula.Components))
	for _, component := range formula.Components {
		components[component.Code] = 1
	}
	// A raw discipline percentile of 1 is the worst value and must contribute 0.
	score, err := formula.Score(components, 1)
	if err != nil {
		t.Fatal(err)
	}
	if score != 90_000 {
		t.Fatalf("score = %d, want 90000", score)
	}
}

func TestProgressionScoreIsCenteredAndMinutesAdjusted(t *testing.T) {
	catalog, err := LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	formula, _ := catalog.Formula(MostImprovedPlayer)
	components := make(map[string]float64, len(formula.Components))
	for _, component := range formula.Components {
		components[component.Code] = 0.75
	}

	score, err := formula.Score(components, 0.5)
	if err != nil {
		t.Fatal(err)
	}
	if score != 62_500 {
		t.Fatalf("score = %d, want 62500", score)
	}
}

func TestScoreRejectsMissingOrInvalidInputs(t *testing.T) {
	catalog, err := LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	formula, _ := catalog.Formula(PlayerOfSeason)
	if _, err := formula.Score(map[string]float64{}, 1); err == nil {
		t.Fatal("expected missing component error")
	}

	components := make(map[string]float64, len(formula.Components))
	for _, component := range formula.Components {
		components[component.Code] = 0.5
	}
	if _, err := formula.Score(components, 1.1); err == nil {
		t.Fatal("expected reliability range error")
	}
}
