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

	"gitlab.com/gitlab-org/gitaly/internal/command"
	"gitlab.com/gitlab-org/gitaly/internal/config"
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
	reader, err := command.New(ctx, cmd, nil, nil, nil, os.Environ()...)
	if err != nil {
		return nil, err
	}

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
	tempFile, err := ioutil.TempFile("", "gitaly-linguist")
	if err != nil {
		return err
	}
	defer os.Remove(tempFile.Name())

	if err := tempFile.Close(); err != nil {
		return err
	}

	// We can't use stdout to communicate because Bundler sometimes writes
	// warnings to stdout. So we write the information we need into a
	// tempfile instead.
	rubyScript := `IO.write(ARGV.first, Bundler.rubygems.find_name('github-linguist').first.full_gem_path)`
	cmd := exec.Command("bundle", "exec", "ruby", "-e", rubyScript, tempFile.Name())
	cmd.Dir = config.Config.Ruby.Dir

	if err := cmd.Run(); err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			err = fmt.Errorf("%v; stderr: %q", exitError, exitError.Stderr)
		}
		return err
	}

	linguistPath, err := ioutil.ReadFile(tempFile.Name())
	if err != nil {
		return err
	}

	languageJSON, err := ioutil.ReadFile(path.Join(string(linguistPath), "lib/linguist/languages.json"))
	if err != nil {
		return err
	}

	return json.Unmarshal(languageJSON, &colorMap)
}
