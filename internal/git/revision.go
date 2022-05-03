package git

import (
	"bytes"
	"fmt"
)

func validateRevision(revision []byte, allowEmpty bool) error {
	if !allowEmpty && len(revision) == 0 {
		return fmt.Errorf("empty revision")
	}
	if bytes.HasPrefix(revision, []byte("-")) {
		return fmt.Errorf("revision can't start with '-'")
	}
	if bytes.ContainsAny(revision, " \t\n\r") {
		return fmt.Errorf("revision can't contain whitespace")
	}
	if bytes.Contains(revision, []byte("\x00")) {
		return fmt.Errorf("revision can't contain NUL")
	}
	if bytes.Contains(revision, []byte(":")) {
		return fmt.Errorf("revision can't contain ':'")
	}
	return nil
}

// ValidateRevisionAllowEmpty checks if a revision looks valid, but allows
// empty strings
func ValidateRevisionAllowEmpty(revision []byte) error {
	return validateRevision(revision, true)
}

// ValidateRevision checks if a revision looks valid
func ValidateRevision(revision []byte) error {
	return validateRevision(revision, false)
}
