package cgroups

import (
	"fmt"
)

type CgroupValidationError struct {
	Field string
	Msg   string
}

// ValidationError implements error interface due to this method
func (e CgroupValidationError) Error() string {
	return fmt.Sprintf("%s is invalid: %s", e.Field, e.Msg)
}
