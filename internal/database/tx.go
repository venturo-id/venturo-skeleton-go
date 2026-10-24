package database

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// ErrNotFound is returned by a repository write (UPDATE / DELETE) that
// matched no row. Services translate it into their own not-found error.
var ErrNotFound = errors.New("record not found")

// DBTX runs statements. *pgxpool.Pool and pgx.Tx both satisfy it.
type DBTX interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// TxBeginner starts a transaction (*pgxpool.Pool).
type TxBeginner interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

type txKey struct{}

// WithTx runs fn inside one transaction: it commits when fn returns nil
// and rolls back otherwise. Every repository call made with the ctx that
// fn receives joins the transaction, so a service can make writes across
// several repositories atomic without passing the transaction around.
//
// Called while ctx already carries a transaction, WithTx joins it instead
// of starting a new one.
func WithTx(ctx context.Context, db TxBeginner, fn func(ctx context.Context) error) error {
	if _, ok := ctx.Value(txKey{}).(pgx.Tx); ok {
		return fn(ctx)
	}

	tx, err := db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	// Rollback is a no-op after a successful Commit.
	defer func() { _ = tx.Rollback(ctx) }()

	if err := fn(context.WithValue(ctx, txKey{}, tx)); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

// Querier returns the transaction carried by ctx (see WithTx), or db when
// there is none. Repositories run every statement through it.
func Querier(ctx context.Context, db DBTX) DBTX {
	if tx, ok := ctx.Value(txKey{}).(pgx.Tx); ok {
		return tx
	}
	return db
}

// UniqueViolation reports whether err is a unique-constraint violation and,
// if so, the name of the violated constraint or index. Services check it
// after an insert or update, because an "exists?" query beforehand cannot
// stop two concurrent requests from both passing.
func UniqueViolation(err error) (constraint string, ok bool) {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return pgErr.ConstraintName, true
	}
	return "", false
}
