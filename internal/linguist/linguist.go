package linguist

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path"
	"strings"

	"gitlab.com/gitlab-org/gitaly/internal/config"
	"gitlab.com/gitlab-org/gitaly/internal/helper"
)

var (
	colorMap = make(map[string]Language)
)

// Language is used to parse Linguist's language.json file.
type Language struct {
	Color string `json:"color"`
}

// Stats returns the repository's language stats as reported by 'git-linguist'.
func Stats(ctx context.Context, repoPath string, commitID string) (map[string]int, error) {
	cmd := exec.Command("bundle", "exec", "bin/ruby-cd", repoPath, "git-linguist", "--commit="+commitID, "stats")
	cmd.Dir = config.Config.Ruby.Dir
	reader, err := helper.NewCommand(ctx, cmd, nil, nil, nil, os.Environ()...)
	if err != nil {
		return nil, err
	}
	defer reader.Close()

	data, err := ioutil.ReadAll(reader)
	if err != nil {
		return nil, err
	}

	stats := make(map[string]int)
	return stats, json.Unmarshal(data, &stats)
}

// Color returns the color Linguist has assigned to language.
func Color(language string) string {
	if color := colorMap[language].Color; color != "" {
		return color
	}

	colorSha := sha256.Sum256([]byte(language))
	return fmt.Sprintf("#%x", colorSha[0:3])
}

// LoadColors loads the name->color map from the Linguist gem.
func LoadColors() error {
	cmd := exec.Command("bundle", "show", "linguist")
	cmd.Dir = config.Config.Ruby.Dir
	linguistPath, err := cmd.Output()
	if err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			err = fmt.Errorf("%v; stderr: %q", exitError, exitError.Stderr)
		}
		return err
	}

	languageJSON, err := ioutil.ReadFile(path.Join(strings.TrimSpace(string(linguistPath)), "lib/linguist/languages.json"))
	if err != nil {
		return err
	}

	return json.Unmarshal(languageJSON, &colorMap)
}
