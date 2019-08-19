package cache

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"gitlab.com/gitlab-org/gitaly/internal/config"
)

func TestcleanWalkDirNotExists(t *testing.T) {
	err := cleanWalk(config.Storage{Name: "foo", Path: "/path/that/does/not/exist"})
	assert.NoError(t, err, "cleanWalk returned an error for a non existing directory")
}
