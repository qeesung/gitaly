package nodes

import (
	"google.golang.org/grpc"
)

// MockManager is a helper for tests that implements Manager and allows
// for parametrizing behavior.
type MockManager struct {
	Manager
	GetShardFunc func(string) (Shard, error)
}

func (m *MockManager) GetShard(storage string) (Shard, error) {
	return m.GetShardFunc(storage)
}

// MockNode is a helper for tests that implements Node and allows
// for parametrizing behavior.
type MockNode struct {
	Node
	StorageName string
	Conn        *grpc.ClientConn
}

func (m *MockNode) GetStorage() string { return m.StorageName }

func (m *MockNode) GetConnection() *grpc.ClientConn { return m.Conn }

func (m *MockNode) GetAddress() string { return "" }

func (m *MockNode) GetToken() string { return "" }
