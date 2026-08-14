// Package awards loads and evaluates the immutable season-award formula catalog.
package awards

import (
	"embed"
	"encoding/json"
	"fmt"
	"math"
)

const (
	PlayerOfSeason           = "PLAYER_OF_SEASON"
	DefenderOfSeason         = "DEFENDER_OF_SEASON"
	GoalkeeperOfSeason       = "GOALKEEPER_OF_SEASON"
	YoungPlayerOfSeason      = "YOUNG_PLAYER_OF_SEASON"
	ImpactSubstituteOfSeason = "IMPACT_SUBSTITUTE_OF_SEASON"
	MostImprovedPlayer       = "MOST_IMPROVED_PLAYER"
)

const (
	weightedStandardized       = "weighted_standardized_components"
	pointsScale          int64 = 1000
)

//go:embed formulas/*.json
var formulaFiles embed.FS

// Catalog is one published collection of season-award formulas.
type Catalog struct {
	Specification  string            `json:"specification"`
	CatalogVersion int               `json:"catalog_version"`
	Status         string            `json:"status"`
	Normalization  map[string]any    `json:"normalization"`
	CommonMetrics  map[string]string `json:"common_metrics"`
	Awards         []Formula         `json:"awards"`
}

// Formula is an immutable award definition. Eligibility and metric expressions
// are retained as JSON-compatible data so calculation runs can preserve the
// exact published policy in their breakdown.
type Formula struct {
	ID           string         `json:"id"`
	Code         string         `json:"code"`
	Name         string         `json:"name"`
	Version      int            `json:"version"`
	Status       string         `json:"status"`
	Eligibility  map[string]any `json:"eligibility"`
	Aggregation  string         `json:"aggregation"`
	Reliability  string         `json:"reliability,omitempty"`
	Components   []Component    `json:"components"`
	FinalFormula string         `json:"final_formula,omitempty"`
	TieBreakers  []string       `json:"tie_breakers"`
}

// Component is one normalized input and its contribution to an award.
type Component struct {
	Code      string  `json:"code"`
	Weight    float64 `json:"weight"`
	Direction string  `json:"direction"`
	Metric    string  `json:"metric"`
}

// LoadCatalog loads and validates the active version-three catalog. A fresh
// value is returned on every call so callers cannot mutate shared formula state.
func LoadCatalog() (Catalog, error) {
	payload, err := formulaFiles.ReadFile("formulas/season-v3.json")
	if err != nil {
		return Catalog{}, fmt.Errorf("read season award formulas: %w", err)
	}
	var catalog Catalog
	if err := json.Unmarshal(payload, &catalog); err != nil {
		return Catalog{}, fmt.Errorf("decode season award formulas: %w", err)
	}
	if err := catalog.validate(); err != nil {
		return Catalog{}, err
	}
	return catalog, nil
}

// Formula returns an award definition by stable code.
func (c Catalog) Formula(code string) (Formula, bool) {
	for _, formula := range c.Awards {
		if formula.Code == code {
			return formula, true
		}
	}
	return Formula{}, false
}

// RawScore applies the published formula to already standardized components.
// It is the value used to rank candidates; unlike a percentile it is unbounded
// and preserves the distance between performances.
func (f Formula) RawScore(components map[string]float64) (float64, error) {
	if f.Aggregation != weightedStandardized {
		return 0, fmt.Errorf("unsupported aggregation %q", f.Aggregation)
	}
	weighted := 0.0
	for _, component := range f.Components {
		value, ok := components[component.Code]
		if !ok {
			return 0, fmt.Errorf("missing standardized component %q", component.Code)
		}
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return 0, fmt.Errorf("component %q must be finite", component.Code)
		}
		if component.Direction == "lower" {
			value = -value
		}
		weighted += component.Weight * value
	}
	return weighted, nil
}

// Score converts RawScore to display-only millipoints on a 0..100 logistic
// scale. This monotonic conversion never changes candidate order.
func (f Formula) Score(components map[string]float64) (int64, error) {
	raw, err := f.RawScore(components)
	if err != nil {
		return 0, err
	}
	score := 100 / (1 + math.Exp(-raw))
	return int64(math.Round(score * float64(pointsScale))), nil
}

func (c Catalog) validate() error {
	if c.Specification != "go-football/season-awards" || c.CatalogVersion != 3 || c.Status != "active" {
		return fmt.Errorf("unsupported season award catalog")
	}
	if len(c.Awards) != 6 {
		return fmt.Errorf("season award catalog has %d formulas, want 6", len(c.Awards))
	}
	seen := make(map[string]bool, len(c.Awards))
	for _, formula := range c.Awards {
		if formula.ID == "" || formula.Code == "" || formula.Name == "" || formula.Version != c.CatalogVersion || formula.Status != "active" {
			return fmt.Errorf("invalid formula metadata for %q", formula.Code)
		}
		if seen[formula.Code] {
			return fmt.Errorf("duplicate formula code %q", formula.Code)
		}
		seen[formula.Code] = true
		if len(formula.Components) == 0 || len(formula.TieBreakers) == 0 {
			return fmt.Errorf("formula %q is incomplete", formula.Code)
		}
		weight := 0.0
		components := make(map[string]bool, len(formula.Components))
		for _, component := range formula.Components {
			if component.Code == "" || component.Metric == "" || component.Weight <= 0 {
				return fmt.Errorf("formula %q has an invalid component", formula.Code)
			}
			if component.Direction != "higher" && component.Direction != "lower" {
				return fmt.Errorf("formula %q has invalid direction %q", formula.Code, component.Direction)
			}
			if components[component.Code] {
				return fmt.Errorf("formula %q repeats component %q", formula.Code, component.Code)
			}
			components[component.Code] = true
			weight += component.Weight
		}
		if math.Abs(weight-1) > 1e-9 {
			return fmt.Errorf("formula %q weights total %.6f, want 1", formula.Code, weight)
		}
		if formula.Aggregation != weightedStandardized {
			return fmt.Errorf("formula %q uses unsupported aggregation %q", formula.Code, formula.Aggregation)
		}
	}
	return nil
}
