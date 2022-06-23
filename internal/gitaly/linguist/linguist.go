package linguist

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"gitlab.com/gitlab-org/gitaly/v15/internal/command"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v15/internal/helper/env"
)

// Language is used to parse Linguist's language.json file.
type Language struct {
	Color string `json:"color"`
}

// ByteCountPerLanguage represents a counter value (bytes) per language.
type ByteCountPerLanguage map[string]uint64

// Instance is a holder of the defined in the system language settings.
type Instance struct {
	cfg           config.Cfg
	colorMap      map[string]Language
	gitCmdFactory git.CommandFactory
}

// New loads the name->color map from the Linguist gem and returns initialised instance
// to use back to the caller or an error.
func New(cfg config.Cfg, gitCmdFactory git.CommandFactory) (*Instance, error) {
	jsonReader, err := openLanguagesJSON(cfg)
	if err != nil {
		return nil, err
	}
	defer jsonReader.Close()

	var colorMap map[string]Language
	if err := json.NewDecoder(jsonReader).Decode(&colorMap); err != nil {
		return nil, err
	}

	return &Instance{
		cfg:           cfg,
		gitCmdFactory: gitCmdFactory,
		colorMap:      colorMap,
	}, nil
}

// Stats returns the repository's language stats as reported by 'git-linguist'.
func (inst *Instance) Stats(ctx context.Context, repoPath string, commitID string) (ByteCountPerLanguage, error) {
	cmd, err := inst.startGitLinguist(ctx, repoPath, commitID)
	if err != nil {
		return nil, fmt.Errorf("starting linguist: %w", err)
	}

	data, err := io.ReadAll(cmd)
	if err != nil {
		return nil, fmt.Errorf("reading linguist output: %w", err)
	}

	if err := cmd.Wait(); err != nil {
		return nil, fmt.Errorf("waiting for linguist: %w", err)
	}

	stats := make(ByteCountPerLanguage)
	if err := json.Unmarshal(data, &stats); err != nil {
		return nil, fmt.Errorf("unmarshaling stats: %w", err)
	}

	return stats, nil
}

// Color returns the color Linguist has assigned to language.
func (inst *Instance) Color(language string) string {
	if color := inst.colorMap[language].Color; color != "" {
		return color
	}

	colorSha := sha256.Sum256([]byte(language))
	return fmt.Sprintf("#%x", colorSha[0:3])
}

func (inst *Instance) startGitLinguist(ctx context.Context, repoPath string, commitID string) (*command.Command, error) {
	bundle, err := exec.LookPath("bundle")
	if err != nil {
		return nil, fmt.Errorf("finding bundle executable: %w", err)
	}

	cmd := exec.Command(bundle, "exec", "bin/gitaly-linguist", "--repository="+repoPath, "--commit="+commitID)
	cmd.Dir = inst.cfg.Ruby.Dir

	internalCmd, err := command.New(ctx, cmd,
		command.WithEnvironment(env.AllowedRubyEnvironment(os.Environ())),
		command.WithCommandName("git-linguist", "stats"),
	)
	if err != nil {
		return nil, fmt.Errorf("creating command: %w", err)
	}

	return internalCmd, nil
}

func openLanguagesJSON(cfg config.Cfg) (io.ReadCloser, error) {
	if jsonPath := cfg.Ruby.LinguistLanguagesPath; jsonPath != "" {
		// This is a fallback for environments where dynamic discovery of the
		// linguist path via Bundler is not working for some reason, for example
		// https://gitlab.com/gitlab-org/gitaly/issues/1119.
		return os.Open(jsonPath)
	}

	linguistPathSymlink, err := os.CreateTemp("", "gitaly-linguist-path")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.Remove(linguistPathSymlink.Name()) }()

	if err := linguistPathSymlink.Close(); err != nil {
		return nil, err
	}

	// We use a symlink because we cannot trust Bundler to not print garbage
	// on its stdout.
	rubyScript := `FileUtils.ln_sf(Bundler.rubygems.find_name('github-linguist').first.full_gem_path, ARGV.first)`
	cmd := exec.Command("bundle", "exec", "ruby", "-rfileutils", "-e", rubyScript, linguistPathSymlink.Name())
	cmd.Dir = cfg.Ruby.Dir

	// We have learned that in practice the command we are about to run is a
	// canary for Ruby/Bundler configuration problems. Including stderr and
	// stdout in the gitaly log is useful for debugging such problems.
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout

	if err := cmd.Run(); err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			err = fmt.Errorf("%v; stderr: %q", exitError, exitError.Stderr)
		}
		return nil, err
	}

	return os.Open(filepath.Join(linguistPathSymlink.Name(), "lib", "linguist", "languages.json"))
}
