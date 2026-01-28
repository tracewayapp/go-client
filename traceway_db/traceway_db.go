package tracewaydb

import (
	"context"
	"database/sql"

	traceway "go.tracewayapp.com"
)

// So the plan is to wrap the DB types in order to provide instrumentation

type TwDB struct {
	*sql.DB
	ctx context.Context
}

func NewTwDB(ctx context.Context, db *sql.DB) *TwDB {
	return &TwDB{db, ctx}
}

// Functions to override for segments/spans
func (twdb *TwDB) PrepareContext(ctx context.Context, query string) (*sql.Stmt, error) {
	seg := traceway.StartSegment(twdb.ctx, query)
	defer seg.End()
	return twdb.DB.PrepareContext(ctx, query)
}
func (twdb *TwDB) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	seg := traceway.StartSegment(twdb.ctx, query)
	defer seg.End()
	return twdb.DB.ExecContext(ctx, query, args...)
}
func (twdb *TwDB) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	seg := traceway.StartSegment(twdb.ctx, query)
	defer seg.End()
	return twdb.DB.QueryContext(ctx, query, args...)
}
func (twdb *TwDB) Exec(query string, args ...any) (sql.Result, error) {
	seg := traceway.StartSegment(twdb.ctx, query)
	defer seg.End()
	return twdb.DB.Exec(query, args...)
}
func (twdb *TwDB) Query(query string, args ...any) (*sql.Rows, error) {
	seg := traceway.StartSegment(twdb.ctx, query)
	defer seg.End()
	return twdb.DB.Query(query, args...)
}
func (twdb *TwDB) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	seg := traceway.StartSegment(twdb.ctx, query)
	defer seg.End()
	return twdb.DB.QueryRowContext(ctx, query, args...)
}
func (twdb *TwDB) QueryRow(query string, args ...any) *sql.Row {
	seg := traceway.StartSegment(twdb.ctx, query)
	defer seg.End()
	return twdb.DB.QueryRow(query, args...)
}

// Functions to override to returned entities connected to the same ctx as the TwDb
func (twdb *TwDB) Prepare(query string) (*TwStmt, error) {
	stmt, err := twdb.DB.Prepare(query)
	if err != nil {
		return nil, err
	}
	return &TwStmt{stmt, twdb.ctx, query}, nil
}

func (twdb *TwDB) Begin() (*TwTx, error) {
	tx, err := twdb.DB.Begin()
	if err != nil {
		return nil, err
	}
	return &TwTx{tx, twdb.ctx}, nil
}
func (twdb *TwDB) Conn(ctx context.Context) (*TwConn, error) {
	conn, err := twdb.DB.Conn(ctx)
	if err != nil {
		return nil, err
	}
	return &TwConn{conn, twdb.ctx}, nil

}
func (twdb *TwDB) BeginTx(ctx context.Context, opts *sql.TxOptions) (*TwTx, error) {
	tx, err := twdb.DB.BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return &TwTx{tx, twdb.ctx}, nil
}

type TwStmt struct {
	*sql.Stmt
	ctx      context.Context
	queryStr string
}

func (tws *TwStmt) Exec(args ...any) (sql.Result, error) {
	seg := traceway.StartSegment(tws.ctx, tws.queryStr)
	defer seg.End()
	return tws.Stmt.Exec(args...)
}
func (tws *TwStmt) ExecContext(ctx context.Context, args ...any) (sql.Result, error) {
	seg := traceway.StartSegment(tws.ctx, tws.queryStr)
	defer seg.End()
	return tws.Stmt.ExecContext(ctx, args...)
}
func (tws *TwStmt) QueryContext(ctx context.Context, args ...any) (*sql.Rows, error) {
	seg := traceway.StartSegment(tws.ctx, tws.queryStr)
	defer seg.End()
	return tws.Stmt.QueryContext(ctx, args...)
}
func (tws *TwStmt) Query(args ...any) (*sql.Rows, error) {
	seg := traceway.StartSegment(tws.ctx, tws.queryStr)
	defer seg.End()
	return tws.Stmt.Query(args...)
}
func (tws *TwStmt) QueryRowContext(ctx context.Context, args ...any) *sql.Row {
	seg := traceway.StartSegment(tws.ctx, tws.queryStr)
	defer seg.End()
	return tws.Stmt.QueryRowContext(ctx, args...)
}
func (tws *TwStmt) QueryRow(args ...any) *sql.Row {
	seg := traceway.StartSegment(tws.ctx, tws.queryStr)
	defer seg.End()
	return tws.Stmt.QueryRow(args...)
}

type TwTx struct {
	*sql.Tx
	ctx context.Context
}

func NewTwTx(ctx context.Context, tx *sql.Tx) *TwTx {
	return &TwTx{tx, ctx}
}

func (twtx *TwTx) PrepareContext(ctx context.Context, query string) (*sql.Stmt, error) {
	seg := traceway.StartSegment(twtx.ctx, query)
	defer seg.End()
	return twtx.Tx.PrepareContext(ctx, query)
}
func (twtx *TwTx) Prepare(query string) (*sql.Stmt, error) {
	seg := traceway.StartSegment(twtx.ctx, query)
	defer seg.End()
	return twtx.Tx.Prepare(query)
}
func (twtx *TwTx) StmtContext(ctx context.Context, stmt *sql.Stmt) *TwStmt {
	return &TwStmt{twtx.Tx.StmtContext(ctx, stmt), twtx.ctx, "Unknown Query (stmt from stmt)"}
}
func (twtx *TwTx) Stmt(stmt *sql.Stmt) *TwStmt {
	return &TwStmt{twtx.Tx.Stmt(stmt), twtx.ctx, "Unknown Query (stmt from stmt)"}
}
func (twtx *TwTx) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	seg := traceway.StartSegment(twtx.ctx, query)
	defer seg.End()
	return twtx.Tx.ExecContext(ctx, query, args...)
}
func (twtx *TwTx) Exec(query string, args ...any) (sql.Result, error) {
	seg := traceway.StartSegment(twtx.ctx, query)
	defer seg.End()
	return twtx.Tx.Exec(query, args...)
}
func (twtx *TwTx) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	seg := traceway.StartSegment(twtx.ctx, query)
	defer seg.End()
	return twtx.Tx.QueryContext(ctx, query, args...)
}
func (twtx *TwTx) Query(query string, args ...any) (*sql.Rows, error) {
	seg := traceway.StartSegment(twtx.ctx, query)
	defer seg.End()
	return twtx.Tx.Query(query, args...)
}
func (twtx *TwTx) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	seg := traceway.StartSegment(twtx.ctx, query)
	defer seg.End()
	return twtx.Tx.QueryRowContext(ctx, query, args...)
}
func (twtx *TwTx) QueryRow(query string, args ...any) *sql.Row {
	seg := traceway.StartSegment(twtx.ctx, query)
	defer seg.End()
	return twtx.Tx.QueryRow(query, args...)
}

type TwConn struct {
	*sql.Conn
	ctx context.Context
}

func (twc *TwConn) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	seg := traceway.StartSegment(twc.ctx, query)
	defer seg.End()
	return twc.Conn.ExecContext(ctx, query, args...)
}
func (twc *TwConn) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	seg := traceway.StartSegment(twc.ctx, query)
	defer seg.End()
	return twc.Conn.QueryContext(ctx, query, args...)
}
func (twc *TwConn) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	seg := traceway.StartSegment(twc.ctx, query)
	defer seg.End()
	return twc.Conn.QueryRowContext(ctx, query, args...)
}
func (twc *TwConn) PrepareContext(ctx context.Context, query string) (*sql.Stmt, error) {
	seg := traceway.StartSegment(twc.ctx, query)
	defer seg.End()
	return twc.Conn.PrepareContext(ctx, query)
}
func (twc *TwConn) Raw(f func(driverConn any) error) (err error) {
	seg := traceway.StartSegment(twc.ctx, "Raw Db Driver Interaction")
	defer seg.End()
	return twc.Conn.Raw(f)
}
func (twc *TwConn) BeginTx(ctx context.Context, opts *sql.TxOptions) (*TwTx, error) {
	tx, err := twc.Conn.BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return &TwTx{tx, twc.ctx}, nil
}
