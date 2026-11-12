package db

import (
	"context"
	"fmt"

	sq "github.com/Masterminds/squirrel"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/weeks6/travel-docs/internal/paging"
)

// Querier runs queries; both *pgxpool.Pool and pgx.Tx implement it, so
// helpers taking a Querier work inside and outside transactions.
type Querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// ListPage counts the rows of count, then loads one page of list (to which
// it adds LIMIT and OFFSET) and scans each row with scan.
func ListPage[T any](ctx context.Context, q Querier, count, list sq.SelectBuilder, p paging.Params,
	scan func(pgx.Row) (T, error)) (paging.Page[T], error) {
	res := paging.Empty[T](p)
	countSQL, countArgs, err := count.ToSql()
	if err != nil {
		return res, err
	}
	var total int
	if err := q.QueryRow(ctx, countSQL, countArgs...).Scan(&total); err != nil {
		return res, fmt.Errorf("count: %w", err)
	}
	res.SetTotal(total)

	listSQL, listArgs, err := list.Limit(uint64(p.PageSize)).Offset(uint64(p.Offset())).ToSql()
	if err != nil {
		return res, err
	}
	rows, err := q.Query(ctx, listSQL, listArgs...)
	if err != nil {
		return res, fmt.Errorf("list: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		item, err := scan(rows)
		if err != nil {
			return res, err
		}
		res.Items = append(res.Items, item)
	}
	return res, rows.Err()
}
