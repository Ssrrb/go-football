package awards

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

const defaultCandidateLimit = 3

// Candidate is one player ranked in a published season-award selection.
type Candidate struct {
	AwardCode  string
	Rank       int
	PlayerID   string
	PlayerName string
	TeamName   string
	ScoreMilli int64
	Projection bool
}

// Score returns the public decimal representation of the 0–100 award score.
func (c Candidate) Score() string {
	return fmt.Sprintf("%d.%03d", c.ScoreMilli/1000, c.ScoreMilli%1000)
}

// Repository reads published season-award candidates from PostgreSQL.
type Repository struct {
	pool *pgxpool.Pool
}

// NewRepository creates a season-award repository.
func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

// TopCandidates returns up to limit candidates for each requested award code.
// Only the current published selection is visible to the web application.
func (r *Repository) TopCandidates(ctx context.Context, editionID string, awardCodes []string, limit int) (map[string][]Candidate, error) {
	if len(awardCodes) == 0 {
		return map[string][]Candidate{}, nil
	}
	if limit <= 0 || limit > 10 {
		limit = defaultCandidateLimit
	}

	const statement = `
SELECT selections.award_code, candidates.rank, players.id::text,
       players.display_name, COALESCE(candidate_team.name, current_team.name, ''),
       candidates.score_milli
FROM season_award_selection AS selections
JOIN season_award_candidate AS candidates ON candidates.selection_id = selections.id
JOIN player AS players ON players.id = candidates.player_id
LEFT JOIN team AS candidate_team ON candidate_team.id = candidates.team_id
LEFT JOIN LATERAL (
    SELECT teams.name
    FROM player_edition_team AS membership
    JOIN team AS teams ON teams.id = membership.team_id
    WHERE membership.edition_id = selections.edition_id
      AND membership.player_id = candidates.player_id
    ORDER BY membership.last_seen_at DESC, membership.team_id
    LIMIT 1
) AS current_team ON true
WHERE selections.edition_id = $1::uuid
  AND selections.award_code = ANY($2::text[])
  AND selections.state = 'published'
  AND selections.is_current
  AND candidates.rank <= $3
ORDER BY selections.award_code, candidates.rank`

	rows, err := r.pool.Query(ctx, statement, editionID, awardCodes, limit)
	if err != nil {
		return nil, fmt.Errorf("query season award candidates: %w", err)
	}
	defer rows.Close()

	result := make(map[string][]Candidate, len(awardCodes))
	for rows.Next() {
		var candidate Candidate
		if err := rows.Scan(
			&candidate.AwardCode,
			&candidate.Rank,
			&candidate.PlayerID,
			&candidate.PlayerName,
			&candidate.TeamName,
			&candidate.ScoreMilli,
		); err != nil {
			return nil, fmt.Errorf("scan season award candidate: %w", err)
		}
		result[candidate.AwardCode] = append(result[candidate.AwardCode], candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate season award candidates: %w", err)
	}

	missing := make([]string, 0, len(awardCodes))
	for _, code := range awardCodes {
		if len(result[code]) == 0 {
			missing = append(missing, code)
		}
	}
	if len(missing) == 0 {
		return result, nil
	}
	projections, err := r.liveCandidates(ctx, editionID, missing, limit)
	if err != nil {
		return nil, err
	}
	for code, candidates := range projections {
		result[code] = candidates
	}
	return result, nil
}
