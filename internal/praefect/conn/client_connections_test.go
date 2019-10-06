package conn

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRegisterNode(t *testing.T) {
	storageName := "default"
	tcpAddress := "address1"
	clientConn := NewClientConnections()

	_, err := clientConn.GetConnection(storageName)
	require.Equal(t, ErrConnectionNotFound, err)

	require.NoError(t, clientConn.RegisterNode(storageName, fmt.Sprintf("tcp://%s", tcpAddress)))

	conn, err := clientConn.GetConnection(storageName)
	require.NoError(t, err)
	require.Equal(t, tcpAddress, conn.Target())

	err = clientConn.RegisterNode(storageName, "tcp://some-other-address")
	require.Equal(t, ErrAlreadyRegistered, err)
}
