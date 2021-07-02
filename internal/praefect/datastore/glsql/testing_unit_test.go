package glsql

import (
	"database/sql"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRunPostgres(t *testing.T) {
	pgCont, err := RunPostgres()
	require.NoError(t, err)
	defer func() { require.NoError(t, pgCont.Close()) }()

	db, err := sql.Open("postgres", "user=postgres sslmode=disable port="+pgCont.Port)
	require.NoError(t, err, "database should accept new connections")
	defer func() { require.NoError(t, db.Close()) }()

	require.NoError(t, db.Ping())
}
