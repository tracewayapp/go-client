package tracewaydb

import (
	"context"
	"database/sql"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	_ "github.com/mattn/go-sqlite3"
	traceway "go.tracewayapp.com"
)

// Helper function to setup TwDB with trace context
func setupTwDBWithTxn(t *testing.T) (*TwDB, sqlmock.Sqlmock, *traceway.TraceContext, func()) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to create sqlmock: %v", err)
	}

	txn := &traceway.TraceContext{Id: "test-trace-id"}
	ctx := context.WithValue(context.Background(), string(traceway.CtxTraceKey), txn)
	twdb := NewTwDB(ctx, db)

	cleanup := func() {
		db.Close()
	}
	return twdb, mock, txn, cleanup
}

// Helper function to setup TwDB without trace context
func setupTwDBWithoutTxn(t *testing.T) (*TwDB, sqlmock.Sqlmock, func()) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to create sqlmock: %v", err)
	}

	twdb := NewTwDB(context.Background(), db)

	cleanup := func() {
		db.Close()
	}
	return twdb, mock, cleanup
}

// Helper function to assert a segment was created with expected name
func assertSegmentCreated(t *testing.T, txn *traceway.TraceContext, expectedName string) {
	t.Helper()
	segments := txn.GetSegments()
	if len(segments) == 0 {
		t.Fatalf("expected at least one segment, got none")
	}

	lastSegment := segments[len(segments)-1]
	if lastSegment.Name != expectedName {
		t.Errorf("expected segment name %q, got %q", expectedName, lastSegment.Name)
	}
	if lastSegment.Duration <= 0 {
		t.Errorf("expected positive duration, got %v", lastSegment.Duration)
	}
}

// Helper function to get segment count
func getSegmentCount(txn *traceway.TraceContext) int {
	return len(txn.GetSegments())
}

// Helper to quote regex special characters for sqlmock
func quoteMeta(s string) string {
	return regexp.QuoteMeta(s)
}

// ==================== TwDB Tests ====================

func TestNewTwDB(t *testing.T) {
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to create sqlmock: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	twdb := NewTwDB(ctx, db)

	if twdb.DB != db {
		t.Error("TwDB.DB should be set to the provided db")
	}
	if twdb.ctx != ctx {
		t.Error("TwDB.ctx should be set to the provided context")
	}
}

func TestTwDB_PrepareContext_Success(t *testing.T) {
	twdb, mock, txn, cleanup := setupTwDBWithTxn(t)
	defer cleanup()

	query := "SELECT * FROM users WHERE id = ?"
	mock.ExpectPrepare(quoteMeta(query))

	stmt, err := twdb.PrepareContext(context.Background(), query)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stmt == nil {
		t.Fatal("expected non-nil statement")
	}
	defer stmt.Close()

	assertSegmentCreated(t, txn, query)

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestTwDB_PrepareContext_Error(t *testing.T) {
	twdb, mock, txn, cleanup := setupTwDBWithTxn(t)
	defer cleanup()

	query := "INVALID SQL"
	mock.ExpectPrepare(quoteMeta(query)).WillReturnError(sql.ErrConnDone)

	_, err := twdb.PrepareContext(context.Background(), query)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	// Segment should still be created even on error
	assertSegmentCreated(t, txn, query)
}

func TestTwDB_ExecContext_Success(t *testing.T) {
	twdb, mock, txn, cleanup := setupTwDBWithTxn(t)
	defer cleanup()

	query := "INSERT INTO users (name) VALUES (?)"
	mock.ExpectExec(quoteMeta(query)).WithArgs("alice").WillReturnResult(sqlmock.NewResult(1, 1))

	result, err := twdb.ExecContext(context.Background(), query, "alice")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	lastInsertId, _ := result.LastInsertId()
	if lastInsertId != 1 {
		t.Errorf("expected lastInsertId 1, got %d", lastInsertId)
	}

	assertSegmentCreated(t, txn, query)

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestTwDB_ExecContext_Error(t *testing.T) {
	twdb, mock, txn, cleanup := setupTwDBWithTxn(t)
	defer cleanup()

	query := "INSERT INTO users (name) VALUES (?)"
	mock.ExpectExec(quoteMeta(query)).WithArgs("alice").WillReturnError(sql.ErrConnDone)

	_, err := twdb.ExecContext(context.Background(), query, "alice")
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	// Segment should still be created even on error
	assertSegmentCreated(t, txn, query)
}

func TestTwDB_ExecContext_NoTxnContext(t *testing.T) {
	twdb, mock, cleanup := setupTwDBWithoutTxn(t)
	defer cleanup()

	query := "INSERT INTO users (name) VALUES (?)"
	mock.ExpectExec(quoteMeta(query)).WithArgs("alice").WillReturnResult(sqlmock.NewResult(1, 1))

	result, err := twdb.ExecContext(context.Background(), query, "alice")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	lastInsertId, _ := result.LastInsertId()
	if lastInsertId != 1 {
		t.Errorf("expected lastInsertId 1, got %d", lastInsertId)
	}

	// Should work without trace context (nil segment is safe)
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestTwDB_Exec_Success(t *testing.T) {
	twdb, mock, txn, cleanup := setupTwDBWithTxn(t)
	defer cleanup()

	query := "UPDATE users SET name = ? WHERE id = ?"
	mock.ExpectExec(quoteMeta(query)).WithArgs("bob", 1).WillReturnResult(sqlmock.NewResult(0, 1))

	result, err := twdb.Exec(query, "bob", 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected != 1 {
		t.Errorf("expected rowsAffected 1, got %d", rowsAffected)
	}

	assertSegmentCreated(t, txn, query)

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestTwDB_QueryContext_Success(t *testing.T) {
	twdb, mock, txn, cleanup := setupTwDBWithTxn(t)
	defer cleanup()

	query := "SELECT id, name FROM users"
	rows := sqlmock.NewRows([]string{"id", "name"}).AddRow(1, "alice").AddRow(2, "bob")
	mock.ExpectQuery(quoteMeta(query)).WillReturnRows(rows)

	result, err := twdb.QueryContext(context.Background(), query)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer result.Close()

	count := 0
	for result.Next() {
		count++
	}
	if count != 2 {
		t.Errorf("expected 2 rows, got %d", count)
	}

	assertSegmentCreated(t, txn, query)

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestTwDB_QueryContext_Error(t *testing.T) {
	twdb, mock, txn, cleanup := setupTwDBWithTxn(t)
	defer cleanup()

	query := "SELECT id FROM nonexistent"
	mock.ExpectQuery(quoteMeta(query)).WillReturnError(sql.ErrNoRows)

	_, err := twdb.QueryContext(context.Background(), query)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	// Segment should still be created even on error
	assertSegmentCreated(t, txn, query)
}

func TestTwDB_Query_Success(t *testing.T) {
	twdb, mock, txn, cleanup := setupTwDBWithTxn(t)
	defer cleanup()

	query := "SELECT id FROM users LIMIT 10"
	rows := sqlmock.NewRows([]string{"id"}).AddRow(1).AddRow(2)
	mock.ExpectQuery(quoteMeta(query)).WillReturnRows(rows)

	result, err := twdb.Query(query)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer result.Close()

	assertSegmentCreated(t, txn, query)

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestTwDB_QueryRowContext_Success(t *testing.T) {
	twdb, mock, txn, cleanup := setupTwDBWithTxn(t)
	defer cleanup()

	query := "SELECT name FROM users WHERE id = ?"
	rows := sqlmock.NewRows([]string{"name"}).AddRow("alice")
	mock.ExpectQuery(quoteMeta(query)).WithArgs(1).WillReturnRows(rows)

	row := twdb.QueryRowContext(context.Background(), query, 1)

	var name string
	err := row.Scan(&name)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != "alice" {
		t.Errorf("expected name 'alice', got %q", name)
	}

	assertSegmentCreated(t, txn, query)

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestTwDB_QueryRow_Success(t *testing.T) {
	twdb, mock, txn, cleanup := setupTwDBWithTxn(t)
	defer cleanup()

	query := "SELECT COUNT(id) FROM users"
	rows := sqlmock.NewRows([]string{"count"}).AddRow(42)
	mock.ExpectQuery(quoteMeta(query)).WillReturnRows(rows)

	row := twdb.QueryRow(query)

	var count int
	err := row.Scan(&count)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 42 {
		t.Errorf("expected count 42, got %d", count)
	}

	assertSegmentCreated(t, txn, query)

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestTwDB_Prepare_Success(t *testing.T) {
	twdb, mock, _, cleanup := setupTwDBWithTxn(t)
	defer cleanup()

	query := "SELECT id FROM users WHERE id = ?"
	mock.ExpectPrepare(quoteMeta(query))

	twstmt, err := twdb.Prepare(query)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if twstmt == nil {
		t.Fatal("expected non-nil TwStmt")
	}
	defer twstmt.Close()

	// Prepare does NOT create a segment
	if twstmt.queryStr != query {
		t.Errorf("expected queryStr %q, got %q", query, twstmt.queryStr)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestTwDB_Prepare_Error(t *testing.T) {
	twdb, mock, _, cleanup := setupTwDBWithTxn(t)
	defer cleanup()

	query := "INVALID SQL SYNTAX"
	mock.ExpectPrepare(quoteMeta(query)).WillReturnError(sql.ErrConnDone)

	_, err := twdb.Prepare(query)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestTwDB_Begin_Success(t *testing.T) {
	twdb, mock, _, cleanup := setupTwDBWithTxn(t)
	defer cleanup()

	mock.ExpectBegin()

	twtx, err := twdb.Begin()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if twtx == nil {
		t.Fatal("expected non-nil TwTx")
	}

	// Begin does NOT create a segment
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestTwDB_Begin_Error(t *testing.T) {
	twdb, mock, _, cleanup := setupTwDBWithTxn(t)
	defer cleanup()

	mock.ExpectBegin().WillReturnError(sql.ErrConnDone)

	_, err := twdb.Begin()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestTwDB_BeginTx_Success(t *testing.T) {
	twdb, mock, _, cleanup := setupTwDBWithTxn(t)
	defer cleanup()

	mock.ExpectBegin()

	twtx, err := twdb.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if twtx == nil {
		t.Fatal("expected non-nil TwTx")
	}

	// BeginTx does NOT create a segment
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

// ==================== TwStmt Tests ====================

func setupTwStmt(t *testing.T) (*TwStmt, sqlmock.Sqlmock, *traceway.TraceContext, func()) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to create sqlmock: %v", err)
	}

	txn := &traceway.TraceContext{Id: "test-trace-id"}
	ctx := context.WithValue(context.Background(), string(traceway.CtxTraceKey), txn)

	query := "SELECT id FROM users WHERE id = ?"
	mock.ExpectPrepare(quoteMeta(query))

	stmt, err := db.Prepare(query)
	if err != nil {
		t.Fatalf("failed to prepare statement: %v", err)
	}

	twstmt := &TwStmt{stmt, ctx, query}

	cleanup := func() {
		stmt.Close()
		db.Close()
	}
	return twstmt, mock, txn, cleanup
}

func TestTwStmt_ExecContext_Success(t *testing.T) {
	twstmt, mock, txn, cleanup := setupTwStmt(t)
	defer cleanup()

	mock.ExpectExec("SELECT").WithArgs(1).WillReturnResult(sqlmock.NewResult(0, 1))

	_, err := twstmt.ExecContext(context.Background(), 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertSegmentCreated(t, txn, twstmt.queryStr)

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestTwStmt_Exec_Success(t *testing.T) {
	twstmt, mock, txn, cleanup := setupTwStmt(t)
	defer cleanup()

	mock.ExpectExec("SELECT").WithArgs(1).WillReturnResult(sqlmock.NewResult(0, 1))

	_, err := twstmt.Exec(1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertSegmentCreated(t, txn, twstmt.queryStr)

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestTwStmt_QueryContext_Success(t *testing.T) {
	twstmt, mock, txn, cleanup := setupTwStmt(t)
	defer cleanup()

	rows := sqlmock.NewRows([]string{"id", "name"}).AddRow(1, "alice")
	mock.ExpectQuery("SELECT").WithArgs(1).WillReturnRows(rows)

	result, err := twstmt.QueryContext(context.Background(), 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer result.Close()

	assertSegmentCreated(t, txn, twstmt.queryStr)

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestTwStmt_Query_Success(t *testing.T) {
	twstmt, mock, txn, cleanup := setupTwStmt(t)
	defer cleanup()

	rows := sqlmock.NewRows([]string{"id", "name"}).AddRow(1, "alice")
	mock.ExpectQuery("SELECT").WithArgs(1).WillReturnRows(rows)

	result, err := twstmt.Query(1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer result.Close()

	assertSegmentCreated(t, txn, twstmt.queryStr)

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestTwStmt_QueryRowContext_Success(t *testing.T) {
	twstmt, mock, txn, cleanup := setupTwStmt(t)
	defer cleanup()

	rows := sqlmock.NewRows([]string{"id", "name"}).AddRow(1, "alice")
	mock.ExpectQuery("SELECT").WithArgs(1).WillReturnRows(rows)

	row := twstmt.QueryRowContext(context.Background(), 1)

	var id int
	var name string
	err := row.Scan(&id, &name)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertSegmentCreated(t, txn, twstmt.queryStr)

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestTwStmt_QueryRow_Success(t *testing.T) {
	twstmt, mock, txn, cleanup := setupTwStmt(t)
	defer cleanup()

	rows := sqlmock.NewRows([]string{"id", "name"}).AddRow(1, "alice")
	mock.ExpectQuery("SELECT").WithArgs(1).WillReturnRows(rows)

	row := twstmt.QueryRow(1)

	var id int
	var name string
	err := row.Scan(&id, &name)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertSegmentCreated(t, txn, twstmt.queryStr)

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

// ==================== TwTx Tests ====================

func setupTwTx(t *testing.T) (*TwTx, sqlmock.Sqlmock, *traceway.TraceContext, func()) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to create sqlmock: %v", err)
	}

	txn := &traceway.TraceContext{Id: "test-trace-id"}
	ctx := context.WithValue(context.Background(), string(traceway.CtxTraceKey), txn)

	mock.ExpectBegin()
	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("failed to begin transaction: %v", err)
	}

	twtx := &TwTx{tx, ctx}

	cleanup := func() {
		db.Close()
	}
	return twtx, mock, txn, cleanup
}

func TestTwTx_PrepareContext_Success(t *testing.T) {
	twtx, mock, txn, cleanup := setupTwTx(t)
	defer cleanup()

	query := "INSERT INTO users (name) VALUES (?)"
	mock.ExpectPrepare(quoteMeta(query))

	stmt, err := twtx.PrepareContext(context.Background(), query)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stmt == nil {
		t.Fatal("expected non-nil statement")
	}
	defer stmt.Close()

	assertSegmentCreated(t, txn, query)

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestTwTx_Prepare_Success(t *testing.T) {
	twtx, mock, txn, cleanup := setupTwTx(t)
	defer cleanup()

	query := "DELETE FROM users WHERE id = ?"
	mock.ExpectPrepare(quoteMeta(query))

	stmt, err := twtx.Prepare(query)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stmt == nil {
		t.Fatal("expected non-nil statement")
	}
	defer stmt.Close()

	assertSegmentCreated(t, txn, query)

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestTwTx_StmtContext_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to create sqlmock: %v", err)
	}
	defer db.Close()

	txn := &traceway.TraceContext{Id: "test-trace-id"}
	ctx := context.WithValue(context.Background(), string(traceway.CtxTraceKey), txn)

	query := "SELECT id FROM users"
	mock.ExpectPrepare(quoteMeta(query))
	stmt, err := db.Prepare(query)
	if err != nil {
		t.Fatalf("failed to prepare statement: %v", err)
	}
	defer stmt.Close()

	mock.ExpectBegin()
	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("failed to begin transaction: %v", err)
	}

	twtx := &TwTx{tx, ctx}

	twstmt := twtx.StmtContext(context.Background(), stmt)
	if twstmt == nil {
		t.Fatal("expected non-nil TwStmt")
	}

	// StmtContext does NOT create a segment, but returns TwStmt with unknown query string
	expectedQueryStr := "Unknown Query (stmt from stmt)"
	if twstmt.queryStr != expectedQueryStr {
		t.Errorf("expected queryStr %q, got %q", expectedQueryStr, twstmt.queryStr)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestTwTx_Stmt_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to create sqlmock: %v", err)
	}
	defer db.Close()

	txn := &traceway.TraceContext{Id: "test-trace-id"}
	ctx := context.WithValue(context.Background(), string(traceway.CtxTraceKey), txn)

	query := "SELECT id FROM users"
	mock.ExpectPrepare(quoteMeta(query))
	stmt, err := db.Prepare(query)
	if err != nil {
		t.Fatalf("failed to prepare statement: %v", err)
	}
	defer stmt.Close()

	mock.ExpectBegin()
	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("failed to begin transaction: %v", err)
	}

	twtx := &TwTx{tx, ctx}

	twstmt := twtx.Stmt(stmt)
	if twstmt == nil {
		t.Fatal("expected non-nil TwStmt")
	}

	// Stmt does NOT create a segment, but returns TwStmt with unknown query string
	expectedQueryStr := "Unknown Query (stmt from stmt)"
	if twstmt.queryStr != expectedQueryStr {
		t.Errorf("expected queryStr %q, got %q", expectedQueryStr, twstmt.queryStr)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestTwTx_ExecContext_Success(t *testing.T) {
	twtx, mock, txn, cleanup := setupTwTx(t)
	defer cleanup()

	query := "INSERT INTO users (name) VALUES (?)"
	mock.ExpectExec(quoteMeta(query)).WithArgs("alice").WillReturnResult(sqlmock.NewResult(1, 1))

	result, err := twtx.ExecContext(context.Background(), query, "alice")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	lastInsertId, _ := result.LastInsertId()
	if lastInsertId != 1 {
		t.Errorf("expected lastInsertId 1, got %d", lastInsertId)
	}

	assertSegmentCreated(t, txn, query)

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestTwTx_Exec_Success(t *testing.T) {
	twtx, mock, txn, cleanup := setupTwTx(t)
	defer cleanup()

	query := "UPDATE users SET active = true"
	mock.ExpectExec(quoteMeta(query)).WillReturnResult(sqlmock.NewResult(0, 5))

	result, err := twtx.Exec(query)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected != 5 {
		t.Errorf("expected rowsAffected 5, got %d", rowsAffected)
	}

	assertSegmentCreated(t, txn, query)

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestTwTx_QueryContext_Success(t *testing.T) {
	twtx, mock, txn, cleanup := setupTwTx(t)
	defer cleanup()

	query := "SELECT id, name FROM users"
	rows := sqlmock.NewRows([]string{"id", "name"}).AddRow(1, "alice")
	mock.ExpectQuery(quoteMeta(query)).WillReturnRows(rows)

	result, err := twtx.QueryContext(context.Background(), query)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer result.Close()

	assertSegmentCreated(t, txn, query)

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestTwTx_Query_Success(t *testing.T) {
	twtx, mock, txn, cleanup := setupTwTx(t)
	defer cleanup()

	query := "SELECT id FROM products"
	rows := sqlmock.NewRows([]string{"id", "name"}).AddRow(1, "widget")
	mock.ExpectQuery(quoteMeta(query)).WillReturnRows(rows)

	result, err := twtx.Query(query)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer result.Close()

	assertSegmentCreated(t, txn, query)

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestTwTx_QueryRowContext_Success(t *testing.T) {
	twtx, mock, txn, cleanup := setupTwTx(t)
	defer cleanup()

	query := "SELECT name FROM users WHERE id = ?"
	rows := sqlmock.NewRows([]string{"name"}).AddRow("bob")
	mock.ExpectQuery(quoteMeta(query)).WithArgs(1).WillReturnRows(rows)

	row := twtx.QueryRowContext(context.Background(), query, 1)

	var name string
	err := row.Scan(&name)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != "bob" {
		t.Errorf("expected name 'bob', got %q", name)
	}

	assertSegmentCreated(t, txn, query)

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestTwTx_QueryRow_Success(t *testing.T) {
	twtx, mock, txn, cleanup := setupTwTx(t)
	defer cleanup()

	query := "SELECT COUNT(id) FROM orders"
	rows := sqlmock.NewRows([]string{"count"}).AddRow(100)
	mock.ExpectQuery(quoteMeta(query)).WillReturnRows(rows)

	row := twtx.QueryRow(query)

	var count int
	err := row.Scan(&count)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 100 {
		t.Errorf("expected count 100, got %d", count)
	}

	assertSegmentCreated(t, txn, query)

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

// ==================== TwConn Tests (using SQLite) ====================

func setupTwConnWithSQLite(t *testing.T) (*TwConn, *sql.DB, *traceway.TraceContext, func()) {
	t.Helper()

	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("failed to open SQLite: %v", err)
	}

	// Create a test table
	_, err = db.Exec("CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT)")
	if err != nil {
		t.Fatalf("failed to create table: %v", err)
	}

	txn := &traceway.TraceContext{Id: "test-trace-id"}
	ctx := context.WithValue(context.Background(), string(traceway.CtxTraceKey), txn)

	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatalf("failed to get connection: %v", err)
	}

	twconn := &TwConn{conn, ctx}

	cleanup := func() {
		conn.Close()
		db.Close()
	}
	return twconn, db, txn, cleanup
}

func TestTwConn_ExecContext_Success(t *testing.T) {
	twconn, _, txn, cleanup := setupTwConnWithSQLite(t)
	defer cleanup()

	query := "INSERT INTO users (name) VALUES (?)"
	result, err := twconn.ExecContext(context.Background(), query, "alice")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected != 1 {
		t.Errorf("expected rowsAffected 1, got %d", rowsAffected)
	}

	assertSegmentCreated(t, txn, query)
}

func TestTwConn_QueryContext_Success(t *testing.T) {
	twconn, _, txn, cleanup := setupTwConnWithSQLite(t)
	defer cleanup()

	// Insert test data
	_, err := twconn.ExecContext(context.Background(), "INSERT INTO users (name) VALUES (?)", "alice")
	if err != nil {
		t.Fatalf("failed to insert test data: %v", err)
	}

	initialSegmentCount := getSegmentCount(txn)

	query := "SELECT id, name FROM users"
	rows, err := twconn.QueryContext(context.Background(), query)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		count++
	}
	if count != 1 {
		t.Errorf("expected 1 row, got %d", count)
	}

	// Check that a new segment was created
	if getSegmentCount(txn) != initialSegmentCount+1 {
		t.Error("expected one new segment to be created")
	}

	segments := txn.GetSegments()
	lastSegment := segments[len(segments)-1]
	if lastSegment.Name != query {
		t.Errorf("expected segment name %q, got %q", query, lastSegment.Name)
	}
}

func TestTwConn_QueryRowContext_Success(t *testing.T) {
	twconn, _, txn, cleanup := setupTwConnWithSQLite(t)
	defer cleanup()

	// Insert test data
	_, err := twconn.ExecContext(context.Background(), "INSERT INTO users (name) VALUES (?)", "bob")
	if err != nil {
		t.Fatalf("failed to insert test data: %v", err)
	}

	initialSegmentCount := getSegmentCount(txn)

	query := "SELECT name FROM users WHERE id = ?"
	row := twconn.QueryRowContext(context.Background(), query, 1)

	var name string
	err = row.Scan(&name)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != "bob" {
		t.Errorf("expected name 'bob', got %q", name)
	}

	// Check that a new segment was created
	if getSegmentCount(txn) != initialSegmentCount+1 {
		t.Error("expected one new segment to be created")
	}

	segments := txn.GetSegments()
	lastSegment := segments[len(segments)-1]
	if lastSegment.Name != query {
		t.Errorf("expected segment name %q, got %q", query, lastSegment.Name)
	}
}

func TestTwConn_PrepareContext_Success(t *testing.T) {
	twconn, _, txn, cleanup := setupTwConnWithSQLite(t)
	defer cleanup()

	query := "SELECT id FROM users WHERE id = ?"
	stmt, err := twconn.PrepareContext(context.Background(), query)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer stmt.Close()

	assertSegmentCreated(t, txn, query)
}

func TestTwConn_Raw_Success(t *testing.T) {
	twconn, _, txn, cleanup := setupTwConnWithSQLite(t)
	defer cleanup()

	err := twconn.Raw(func(driverConn any) error {
		// Just verify we can access the raw driver connection
		if driverConn == nil {
			return sql.ErrConnDone
		}
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertSegmentCreated(t, txn, "Raw Db Driver Interaction")
}

func TestTwConn_BeginTx_Success(t *testing.T) {
	twconn, _, _, cleanup := setupTwConnWithSQLite(t)
	defer cleanup()

	twtx, err := twconn.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if twtx == nil {
		t.Fatal("expected non-nil TwTx")
	}

	// BeginTx does NOT create a segment
	// Just verify it returns a valid TwTx
	if twtx.Tx == nil {
		t.Error("expected non-nil underlying Tx")
	}

	// Must rollback or commit to release the connection
	twtx.Rollback()
}

// ==================== Additional Test Categories ====================

// Test that methods work when no trace is in context (nil segment is safe)
func TestNilContextSafety(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to create sqlmock: %v", err)
	}
	defer db.Close()

	// Create TwDB without trace in context
	twdb := NewTwDB(context.Background(), db)

	// All operations should succeed without panic
	query := "SELECT id FROM users"
	rows := sqlmock.NewRows([]string{"id"}).AddRow(1)
	mock.ExpectQuery(quoteMeta(query)).WillReturnRows(rows)

	result, err := twdb.Query(query)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	result.Close()

	insertQuery := "INSERT INTO users VALUES (?)"
	mock.ExpectExec(quoteMeta(insertQuery)).WillReturnResult(sqlmock.NewResult(1, 1))
	_, err = twdb.Exec(insertQuery, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

// Test that multiple operations create multiple segments
func TestMultipleOperationsCreateMultipleSegments(t *testing.T) {
	twdb, mock, txn, cleanup := setupTwDBWithTxn(t)
	defer cleanup()

	// First query
	query1 := "SELECT id FROM users"
	rows1 := sqlmock.NewRows([]string{"id"}).AddRow(1)
	mock.ExpectQuery(quoteMeta(query1)).WillReturnRows(rows1)

	result1, err := twdb.Query(query1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	result1.Close()

	// Second query
	query2 := "SELECT id FROM products"
	rows2 := sqlmock.NewRows([]string{"id"}).AddRow(1)
	mock.ExpectQuery(quoteMeta(query2)).WillReturnRows(rows2)

	result2, err := twdb.Query(query2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	result2.Close()

	// Third exec
	query3 := "INSERT INTO logs (msg) VALUES (?)"
	mock.ExpectExec(quoteMeta(query3)).WithArgs("test").WillReturnResult(sqlmock.NewResult(1, 1))

	_, err = twdb.Exec(query3, "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	segments := txn.GetSegments()
	if len(segments) != 3 {
		t.Errorf("expected 3 segments, got %d", len(segments))
	}

	// Verify segment names
	if segments[0].Name != query1 {
		t.Errorf("expected first segment name %q, got %q", query1, segments[0].Name)
	}
	if segments[1].Name != query2 {
		t.Errorf("expected second segment name %q, got %q", query2, segments[1].Name)
	}
	if segments[2].Name != query3 {
		t.Errorf("expected third segment name %q, got %q", query3, segments[2].Name)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

// Test context propagation: wrapper uses internal ctx for segments, not passed ctx
func TestContextPropagation(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to create sqlmock: %v", err)
	}
	defer db.Close()

	// Create two different trace contexts
	txnInternal := &traceway.TraceContext{Id: "internal-txn"}
	txnExternal := &traceway.TraceContext{Id: "external-txn"}

	ctxInternal := context.WithValue(context.Background(), string(traceway.CtxTraceKey), txnInternal)
	ctxExternal := context.WithValue(context.Background(), string(traceway.CtxTraceKey), txnExternal)

	// Create TwDB with internal context
	twdb := NewTwDB(ctxInternal, db)

	// Execute query with external context
	query := "SELECT id FROM users"
	rows := sqlmock.NewRows([]string{"id"}).AddRow(1)
	mock.ExpectQuery(quoteMeta(query)).WillReturnRows(rows)

	result, err := twdb.QueryContext(ctxExternal, query)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	result.Close()

	// Segment should be created in internal trace context, not external
	internalSegments := txnInternal.GetSegments()
	externalSegments := txnExternal.GetSegments()

	if len(internalSegments) != 1 {
		t.Errorf("expected 1 segment in internal context, got %d", len(internalSegments))
	}
	if len(externalSegments) != 0 {
		t.Errorf("expected 0 segments in external context, got %d", len(externalSegments))
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

// Test TwDB.Conn returns TwConn with correct context
func TestTwDB_Conn_Success(t *testing.T) {
	// Note: We use setupTwConnWithSQLite helper which properly handles SQLite connections.
	// The TwDB.Conn method wraps db.Conn(), so we test the wrapper behavior here.
	twconn, _, txn, cleanup := setupTwConnWithSQLite(t)
	defer cleanup()

	if twconn == nil {
		t.Fatal("expected non-nil TwConn")
	}

	// Verify TwConn wraps the connection correctly
	if twconn.Conn == nil {
		t.Error("expected non-nil underlying Conn")
	}

	// Test that operations create segments
	query := "INSERT INTO users (name) VALUES (?)"
	_, err := twconn.ExecContext(context.Background(), query, "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertSegmentCreated(t, txn, query)
}

// Test TwDB.Conn error handling
func TestTwDB_Conn_Error(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to create sqlmock: %v", err)
	}
	defer db.Close()

	txn := &traceway.TraceContext{Id: "test-trace-id"}
	ctx := context.WithValue(context.Background(), string(traceway.CtxTraceKey), txn)

	twdb := NewTwDB(ctx, db)

	// Close the mock db to simulate an error
	db.Close()

	_, err = twdb.Conn(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

// Test BeginTx error handling
func TestTwDB_BeginTx_Error(t *testing.T) {
	twdb, mock, _, cleanup := setupTwDBWithTxn(t)
	defer cleanup()

	mock.ExpectBegin().WillReturnError(sql.ErrConnDone)

	_, err := twdb.BeginTx(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

// Test TwConn.BeginTx error handling
func TestTwConn_BeginTx_Error(t *testing.T) {
	twconn, _, _, cleanup := setupTwConnWithSQLite(t)
	defer cleanup()

	// Close the connection to simulate an error
	twconn.Close()

	_, err := twconn.BeginTx(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}
