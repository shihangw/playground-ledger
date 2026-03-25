// pg.go — PostgreSQL metadata store.
//
// Cloud SQL holds account metadata only (user→accounts mapping, priorities).
// TigerBeetle holds all balances and executes every transfer.
//
// Each scenario:
//   1. Queries PG for the relevant account IDs.
//   2. Hands those IDs to TigerBeetle.
package main

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// pgSetup creates the metadata table and wipes any previous run data.
func pgSetup(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS user_accounts (
			user_id    BIGINT NOT NULL,
			account_id BIGINT NOT NULL,
			priority   INT    NOT NULL,
			PRIMARY KEY (user_id, priority)
		);
		CREATE INDEX IF NOT EXISTS idx_user_accounts_user ON user_accounts (user_id);
		TRUNCATE user_accounts;
	`)
	return err
}

func pgBulkInsert(ctx context.Context, pool *pgxpool.Pool, rows [][]any) {
	_, err := pool.CopyFrom(ctx,
		pgx.Identifier{"user_accounts"},
		[]string{"user_id", "account_id", "priority"},
		pgx.CopyFromRows(rows),
	)
	if err != nil {
		panic(fmt.Sprintf("pgBulkInsert: %v", err))
	}
}

// ── Scenario 1: Waterfall ─────────────────────────────────────────────────────
//
// Each worker owns nChains "users", each with 4 accounts in priority order.
// lookupAccounts fetches all nChains×4 rows in one ANY() query and returns
// them grouped by chain, so the caller can submit all chains in one TB call.

type pgWaterfallMeta struct {
	userIDs []int64 // one per chain
}

func pgSetupWaterfall(ctx context.Context, pool *pgxpool.Pool, nWorkers, nChains int) []pgWaterfallMeta {
	// user IDs: 1_000_000 + workerID*nChains + chainID
	// account IDs mirror the TB waterfall layout (base 10_000_000, stride nChains*7)
	rows := make([][]any, 0, nWorkers*nChains*4)
	for i := 0; i < nWorkers; i++ {
		for c := 0; c < nChains; c++ {
			userID := int64(1_000_000 + i*nChains + c)
			tbBase := int64(10_000_000 + (i*nChains+c)*7)
			for pri := 0; pri < 4; pri++ {
				rows = append(rows, []any{userID, tbBase + int64(pri), pri})
			}
		}
	}
	const chunkSize = 5000
	for start := 0; start < len(rows); start += chunkSize {
		end := start + chunkSize
		if end > len(rows) {
			end = len(rows)
		}
		pgBulkInsert(ctx, pool, rows[start:end])
	}

	workers := make([]pgWaterfallMeta, nWorkers)
	for i := 0; i < nWorkers; i++ {
		workers[i].userIDs = make([]int64, nChains)
		for c := 0; c < nChains; c++ {
			workers[i].userIDs[c] = int64(1_000_000 + i*nChains + c)
		}
	}
	return workers
}

// lookupAccounts fetches all chains' source account IDs in one query and returns
// them as chainIDs[c][priority] so the caller can submit all chains in one TB call.
func (m *pgWaterfallMeta) lookupAccounts(ctx context.Context, pool *pgxpool.Pool) ([][]int64, error) {
	rows, err := pool.Query(ctx,
		"SELECT user_id, account_id FROM user_accounts WHERE user_id = ANY($1) ORDER BY user_id, priority",
		m.userIDs,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// Index userIDs so we can group results back into chain order.
	idx := make(map[int64]int, len(m.userIDs))
	for c, uid := range m.userIDs {
		idx[uid] = c
	}
	chainIDs := make([][]int64, len(m.userIDs))
	for i := range chainIDs {
		chainIDs[i] = make([]int64, 0, 4)
	}
	for rows.Next() {
		var uid, aid int64
		if err := rows.Scan(&uid, &aid); err != nil {
			return nil, err
		}
		c := idx[uid]
		chainIDs[c] = append(chainIDs[c], aid)
	}
	return chainIDs, rows.Err()
}

// lookupFirstChain returns the 4 source account IDs for the first user in priority
// order. Used as the fallback after a failed optimistic attempt.
func (m *pgWaterfallMeta) lookupFirstChain(ctx context.Context, pool *pgxpool.Pool) ([]int64, error) {
	rows, err := pool.Query(ctx,
		"SELECT account_id FROM user_accounts WHERE user_id = $1 ORDER BY priority",
		m.userIDs[0],
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// ── Scenario 2: Hot account withdrawal ───────────────────────────────────────
//
// One "hot user" owns one primary account (priority 0).
// Metadata lookup: SELECT account_id WHERE user_id = $hot LIMIT 1.

type pgHotMeta struct {
	hotUserID int64
}

func pgSetupHot(ctx context.Context, pool *pgxpool.Pool) pgHotMeta {
	const hotUserID = int64(2_000_000)
	const hotAccountID = int64(20_000_000) // mirrors TB hot account ID

	pgBulkInsert(ctx, pool, [][]any{
		{hotUserID, hotAccountID, 0},
	})
	return pgHotMeta{hotUserID: hotUserID}
}

func (m *pgHotMeta) lookupAccount(ctx context.Context, pool *pgxpool.Pool) (int64, error) {
	var id int64
	err := pool.QueryRow(ctx,
		"SELECT account_id FROM user_accounts WHERE user_id = $1 ORDER BY priority LIMIT 1",
		m.hotUserID,
	).Scan(&id)
	return id, err
}

// ── Scenario 3: Fan-out ───────────────────────────────────────────────────────
//
// Each worker is one "payer user". 1000 "payee users" each own one account.
// Metadata lookup: SELECT account_id WHERE user_id = ANY($payeeUserIDs).

type pgFanoutMeta struct {
	payerUserID  int64
	payeeUserIDs []int64
}

func pgSetupFanout(ctx context.Context, pool *pgxpool.Pool, nWorkers, nDests int) []pgFanoutMeta {
	const payerUserBase = int64(3_000_000)
	const payeeUserBase = int64(4_000_000)
	const tbSourceBase = int64(30_000_000)
	const tbDestBase = int64(31_000_000)

	rows := make([][]any, 0, nWorkers*(1+nDests))
	for i := 0; i < nWorkers; i++ {
		// payer
		rows = append(rows, []any{payerUserBase + int64(i), tbSourceBase + int64(i), 0})
		// payees
		for j := 0; j < nDests; j++ {
			payeeUser := payeeUserBase + int64(i)*int64(nDests) + int64(j)
			tbDest := tbDestBase + int64(i)*int64(nDests) + int64(j)
			rows = append(rows, []any{payeeUser, tbDest, 0})
		}
	}

	// Insert in chunks to stay within CopyFrom buffer limits.
	const chunkSize = 5000
	for start := 0; start < len(rows); start += chunkSize {
		end := start + chunkSize
		if end > len(rows) {
			end = len(rows)
		}
		pgBulkInsert(ctx, pool, rows[start:end])
	}

	workers := make([]pgFanoutMeta, nWorkers)
	for i := 0; i < nWorkers; i++ {
		workers[i].payerUserID = payerUserBase + int64(i)
		workers[i].payeeUserIDs = make([]int64, nDests)
		for j := 0; j < nDests; j++ {
			workers[i].payeeUserIDs[j] = payeeUserBase + int64(i)*int64(nDests) + int64(j)
		}
	}
	return workers
}

// lookupPayeeAccounts returns the TB account ID for each payee user in one query.
// Order is not guaranteed — TB batch does not require it.
func (m *pgFanoutMeta) lookupPayeeAccounts(ctx context.Context, pool *pgxpool.Pool) ([]int64, error) {
	rows, err := pool.Query(ctx,
		"SELECT account_id FROM user_accounts WHERE user_id = ANY($1)",
		m.payeeUserIDs,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	ids := make([]int64, 0, len(m.payeeUserIDs))
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (m *pgFanoutMeta) lookupPayerAccount(ctx context.Context, pool *pgxpool.Pool) (int64, error) {
	var id int64
	err := pool.QueryRow(ctx,
		"SELECT account_id FROM user_accounts WHERE user_id = $1 LIMIT 1",
		m.payerUserID,
	).Scan(&id)
	return id, err
}
