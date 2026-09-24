package service

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sl-bus/backend/internal/fleet/domain"
	fleetrepo "github.com/sl-bus/backend/internal/fleet/repository"
	"github.com/sl-bus/backend/internal/platform/auth"
	platformdb "github.com/sl-bus/backend/internal/platform/db"
)

// searchClaims is the synthetic tenant context cross-operator search runs
// under — the same deliberate, legitimate all-tenant read pattern as
// ops.ApprovalService/ListingService (Phase 1): search is intentionally
// cross-tenant (FR-SRCH-1: "across all approved operators"), restricted
// instead at the query level to approved operators' scheduled trips
// (see fleet_search.sql).
var searchClaims = &auth.Claims{Role: auth.RolePlatformAdmin}

// SearchService backs FR-SRCH-1..3. Unlike every other fleet service, it
// runs unauthenticated (AUTHENTICATION_DESIGN.md §2.1 — no login wall
// before browsing).
type SearchService struct {
	pool *pgxpool.Pool
}

func NewSearchService(pool *pgxpool.Pool) *SearchService {
	return &SearchService{pool: pool}
}

func (s *SearchService) Search(ctx context.Context, criteria domain.SearchCriteria) ([]domain.TripSearchResult, string, error) {
	var results []domain.TripSearchResult
	var nextCursor string
	err := platformdb.WithTenantContext(ctx, s.pool, searchClaims, func(ctx context.Context, tx pgx.Tx) error {
		repo := fleetrepo.NewSearchRepository(tx)
		var err error
		results, nextCursor, err = repo.Search(ctx, criteria)
		return err
	})
	return results, nextCursor, err
}
