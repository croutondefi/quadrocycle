package db

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type ITx interface {
	RunInTx(context.Context, func(ctx context.Context, tx Tx) error) error
}

type useTx struct {
	client *pgxpool.Pool
}

type Tx interface {
	pgx.Tx
}

func (t *useTx) RunInTx(ctx context.Context, f func(ctx context.Context, tx Tx) error) error {
	tx, err := t.client.Begin(ctx)
	if err != nil {
		return err
	}

	err = f(ctx, tx)

	if err != nil {
		tx.Rollback(ctx)
		return err
	}

	tx.Commit(ctx)

	return nil
}
