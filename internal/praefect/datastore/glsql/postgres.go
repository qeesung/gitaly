// Package glsql (Gitaly SQL) is a helper package to work with plain SQL queries.
package glsql

import (
	"context"
	"database/sql"
	"strconv"
	"strings"

	// Blank import to enable integration of github.com/lib/pq into database/sql
	_ "github.com/lib/pq"
	migrate "github.com/rubenv/sql-migrate"
	"github.com/sirupsen/logrus"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/config"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/datastore/migrations"
)

// OpenDB returns connection pool to the database.
func OpenDB(conf config.DB) (*sql.DB, error) {
	db, err := sql.Open("postgres", conf.ToPQString())
	if err != nil {
		return nil, err
	}

	if err := db.Ping(); err != nil {
		return nil, err
	}

	return db, nil
}

// Migrate will apply all pending SQL migrations.
func Migrate(db *sql.DB, ignoreUnknown bool) (int, error) {
	migrationSource := &migrate.MemoryMigrationSource{Migrations: migrations.All()}
	migrate.SetIgnoreUnknown(ignoreUnknown)
	return migrate.Exec(db, "postgres", migrationSource, migrate.Up)
}

// Querier is an abstraction on *sql.DB and *sql.Tx that allows to use their methods without awareness about actual type.
type Querier interface {
	QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row
}

// TxQuery runs operations inside transaction and commits|rollbacks on Done.
type TxQuery interface {
	// Exec calls op function with provided ctx.
	// Returns true on success and false in case operation failed or wasn't called because of previously failed op.
	Exec(ctx context.Context, op func(context.Context, *sql.Tx) error) bool
	// Done must be called after work is finished to complete transaction.
	// errPtr must not be nil.
	// COMMIT will be executed if no errors happen during TxQuery usage.
	// Otherwise it will be ROLLBACK operation.
	Done(errPtr *error)
}

// NewTxQuery creates entity that allows to run queries in scope of a transaction.
// It always returns non-nil value.
func NewTxQuery(ctx context.Context, logger logrus.FieldLogger, db *sql.DB) TxQuery {
	tx, err := db.BeginTx(ctx, nil)
	return &txQuery{
		tx:     tx,
		err:    err,
		logger: logger,
	}
}

type txQuery struct {
	tx     *sql.Tx
	err    error
	logger logrus.FieldLogger
}

// Exec calls op function with provided ctx.
// Returns true on success and false in case operation failed or wasn't called because of previously failed op.
func (txq *txQuery) Exec(ctx context.Context, op func(context.Context, *sql.Tx) error) bool {
	if txq.err != nil {
		return false
	}

	txq.err = op(ctx, txq.tx)
	return txq.err == nil
}

// Done must be called after work is finished to complete transaction.
// errPtr must not be nil.
// COMMIT will be executed if no errors happen during txQuery usage.
// Otherwise it will be ROLLBACK operation.
func (txq *txQuery) Done(errPtr *error) {
	if txq.err == nil {
		txq.err = txq.tx.Commit()
		if txq.err != nil {
			txq.log(txq.err, "commit failed")
		}
	} else {
		// Don't overwrite txq.err because it's already non-nil
		if err := txq.tx.Rollback(); err != nil {
			txq.log(err, "rollback failed")
		}
	}

	if *errPtr == nil {
		*errPtr = txq.err
	}
}

func (txq *txQuery) log(err error, msg string) {
	if txq.logger != nil {
		txq.logger.WithError(err).Error(msg)
	}
}

// Uint64sToInterfaces converts list of uint64 values to the list of empty interfaces.
func Uint64sToInterfaces(vs ...uint64) []interface{} {
	if vs == nil {
		return nil
	}

	rs := make([]interface{}, len(vs))
	for i, v := range vs {
		rs[i] = v
	}
	return rs
}

// GeneratePlaceholders returns string with 'count' placeholders starting from 'start' index.
// 1 will be used if provided value for 'start' is less then 1.
// 1 will be used if provided value for 'count' is less then 1.
func GeneratePlaceholders(start, count int) string {
	if start < 1 {
		start = 1
	}

	if count <= 1 {
		return "$" + strconv.Itoa(start)
	}

	var builder = strings.Builder{}
	for i := start; i < start+count; i++ {
		if i != start {
			builder.WriteString(",")
		}
		builder.WriteString("$")
		builder.WriteString(strconv.Itoa(i))
	}
	return builder.String()
}

// NewParamsAssembler returns
func NewParamsAssembler() *ParamsAssembler {
	return &ParamsAssembler{}
}

// ParamsAssembler helps to assemble parameters of the query together providing placeholders that must be used in query.
type ParamsAssembler []interface{}

// AddParams receives n params and assemble them with other params and returns generated placeholder as a result.
func (pm *ParamsAssembler) AddParams(params []interface{}) string {
	start := len(*pm)
	*pm = append(*pm, params...)
	return GeneratePlaceholders(start+1, len(params))
}

// AddParam receives param and assemble it with other params and returns generated placeholder as a result.
func (pm *ParamsAssembler) AddParam(param interface{}) string {
	return pm.AddParams([]interface{}{param})
}

// Params returns list of previously assembled parameters.
func (pm *ParamsAssembler) Params() []interface{} {
	if pm != nil {
		return *pm
	}
	return nil
}

// DestProvider returns list of pointers that will be used to scan values into.
type DestProvider interface {
	// To returns list of pointers.
	// It is not an idempotent operation and each call will return a new list.
	To() []interface{}
}

// ScanAll reads all data from 'rows' into holders provided by 'in'.
// It will also 'Close' source after completion.
func ScanAll(rows *sql.Rows, in DestProvider) (err error) {
	defer func() {
		if cErr := rows.Close(); cErr != nil && err == nil {
			err = cErr
		}
	}()

	for rows.Next() {
		if err = rows.Scan(in.To()...); err != nil {
			return err
		}
	}
	err = rows.Err()
	return err
}

// Uint64Provider allows to use it with ScanAll function to read all rows into it and return result as a slice.
type Uint64Provider []*uint64

// Values returns list of values read from *sql.Rows
func (p *Uint64Provider) Values() []uint64 {
	if len(*p) == 0 {
		return nil
	}

	r := make([]uint64, len(*p))
	for i, v := range *p {
		r[i] = *v
	}
	return r
}

// To returns a list of pointers that will be used as a destination for scan operation.
func (p *Uint64Provider) To() []interface{} {
	var d uint64
	*p = append(*p, &d)
	return []interface{}{&d}
}

// StringProvider allows ScanAll to read all rows and return the result as a slice.
type StringProvider []*string

// Values returns list of values read from *sql.Rows
func (p *StringProvider) Values() []string {
	if len(*p) == 0 {
		return nil
	}

	r := make([]string, len(*p))
	for i, v := range *p {
		r[i] = *v
	}
	return r
}

// To returns a list of pointers that will be used as a destination for scan operation.
func (p *StringProvider) To() []interface{} {
	var d string
	*p = append(*p, &d)
	return []interface{}{&d}
}
