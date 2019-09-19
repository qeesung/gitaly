package git

// StaticOption are reusable trusted options
type StaticOption string

// IsOption is a method present on all Flag interface implementations
func (sa StaticOption) IsOption() {}

// ValidateArgs just passes through the already trusted value. This never
// returns an error.
func (sa StaticOption) ValidateArgs() ([]string, error) { return []string{string(sa)}, nil }

const (
	// OutputToStdout is used indicate the output should be sent to STDOUT
	// Seen in: git bundle create
	OutputToStdout = StaticOption("-")
)
