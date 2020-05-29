package config

import (
	"encoding/json"
	"fmt"
)

// Node describes an address that serves a storage
type Node struct {
	Storage        string `toml:"storage"`
	Address        string `toml:"address"`
	Token          string `toml:"token"`
	DefaultPrimary bool   `toml:"primary"`
}

func (n Node) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]interface{}{
		"storage": n.Storage,
		"address": n.Address,
		"primary": n.DefaultPrimary,
	})
}

// String prints out the node attributes but hiding the token
func (n Node) String() string {
	return fmt.Sprintf("storage_name: %s, address: %s, primary: %v", n.Storage, n.Address, n.DefaultPrimary)
}
