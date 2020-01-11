package models

// Node describes an address that serves a storage
type Node struct {
	Storage        string `toml:"storage"`
	Address        string `toml:"address"`
	Token          string `toml:"token"`
	DefaultPrimary bool   `toml:"primary"`
}

// Repository describes a repository's relative path and its primary and list of secondaries
type Repository struct {
	RelativePath string
	Primary      Node
	Replicas     []Node
}

func (r Repository) Clone() Repository {
	clone := r
	clone.Replicas = make([]Node, len(r.Replicas))
	copy(clone.Replicas, r.Replicas)
	return clone
}
