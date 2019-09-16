package git

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	"gitlab.com/gitlab-org/gitaly/internal/command"
	"gitlab.com/gitlab-org/gitaly/internal/git/repository"
)

var invalidationTotal = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "gitaly_invalid_commands_total",
		Help: "Total number of invalid arguments tried to execute",
	},
	[]string{"command"},
)

func init() {
	prometheus.MustRegister(invalidationTotal)
}

func incrInvalidArg(subcmdName string) {
	invalidationTotal.WithLabelValues(subcmdName).Inc()
}

// SubCmd represents a specific git command
type SubCmd struct {
	Name        string   // e.g. "log", or "cat-file", or "worktree"
	Flags       []Option // optional flags before the positional args
	Args        []string // positional args after all flags
	PostSepArgs []string // post separator (i.e. "--") positional args
}

var subCmdNameRegex = regexp.MustCompile(`^[[:alnum:]]+(-[[:alnum:]]+)*$`)

// ValidateArgs checks all arguments in the sub command and validates them
func (sc SubCmd) ValidateArgs() ([]string, error) {
	var safeArgs []string

	if !subCmdNameRegex.MatchString(sc.Name) {
		return nil, &invalidArgErr{
			msg: fmt.Sprintf("invalid sub command name %q", sc.Name),
		}
	}
	safeArgs = append(safeArgs, sc.Name)

	for _, o := range sc.Flags {
		args, err := o.ValidateArgs()
		if err != nil {
			return nil, err
		}
		safeArgs = append(safeArgs, args...)
	}

	for _, a := range sc.Args {
		if err := validatePositionalArg(a); err != nil {
			return nil, err
		}
		safeArgs = append(safeArgs, a)
	}

	if len(sc.PostSepArgs) > 0 {
		safeArgs = append(safeArgs, "--")
	}

	// post separator args do not need any validation
	safeArgs = append(safeArgs, sc.PostSepArgs...)

	return safeArgs, nil
}

// Option is a git command line flag with validation logic
type Option interface {
	IsOption()
	ValidateArgs() ([]string, error)
}

// Flag is a single token optional command line argument that enables or
// disables functionality (e.g. "-L")
type Flag struct {
	Name string
}

// IsOption is a method present on all Flag interface implementations
func (Flag) IsOption() {}

// ValidateArgs returns an error if the flag is not sanitary
func (f1 Flag) ValidateArgs() ([]string, error) {
	if err := validateFlag(f1.Name); err != nil {
		return nil, err
	}
	return []string{f1.Name}, nil
}

// ValueFlag is an optional command line argument that is comprised of pair of
// tokens (e.g. "-n 50")
type ValueFlag struct {
	Name  string
	Value string
}

// IsOption is a method present on all Flag interface implementations
func (ValueFlag) IsOption() {}

// ValidateArgs returns an error if the flag is not sanitary
func (vf ValueFlag) ValidateArgs() ([]string, error) {
	if err := validateFlag(vf.Name); err != nil {
		return nil, err
	}
	return []string{vf.Name, vf.Value}, nil
}

// flagRegex makes sure that a flag follows these rules:
// 1. Can be a short flag (e.g. "-L" or "-a")
// 1.1 Short flags cannot be combined (e.g. "-aBc")
// 2. Can be a long flag (e.g. "--long-flag")
// 2.1 Long flags cannot end with a dash. Interior dashes cannot repeat more
//     than once consecutively.
var flagRegex = regexp.MustCompile(
	`^((-[[:alnum:]])|(--[[:alnum:]]+(-[[:alnum:]]+)*))$`,
)

var flagComboRegex = regexp.MustCompile(
	`^((-[[:alnum:]])|(--[[:alnum:]]+(-[[:alnum:]]+)*))=(.+)$`,
)

// FlagCombo is an optional command line argument comprised of a single token
// that contains both a flag name and value separated by an equal character
// (e.g. "--date=local")
type FlagCombo struct {
	NameValue string
}

// IsOption is a method present on all Flag interface implementations
func (FlagCombo) IsOption() {}

// ValidateArgs returns an error if the flag is not sanitary
func (fc FlagCombo) ValidateArgs() ([]string, error) {
	if !flagComboRegex.MatchString(fc.NameValue) {
		return nil, &invalidArgErr{
			msg: fmt.Sprintf("combination flag %q failed validation", fc.NameValue),
		}
	}
	return []string{fc.NameValue}, nil
}

type invalidArgErr struct {
	msg string
}

func (iae *invalidArgErr) Error() string { return iae.msg }

// IsInvalidArgErr relays if the error is due to an argument validation failure
func IsInvalidArgErr(err error) bool {
	_, ok := err.(*invalidArgErr)
	return ok
}

func validatePositionalArg(arg string) error {
	if strings.HasPrefix(arg, "-") {
		return &invalidArgErr{
			msg: fmt.Sprintf("positional arg %q cannot start with dash '-'", arg),
		}
	}
	return nil
}

func validateFlag(flag string) error {
	if !flagRegex.MatchString(flag) {
		return &invalidArgErr{
			msg: fmt.Sprintf("flag %q failed regex validation", flag),
		}
	}
	return nil
}

// SafeCmd creates a git.Command with the given args and Repository. It
// validates the arguments in the command before executing.
func SafeCmd(ctx context.Context, repo repository.GitRepo, globals []Option, sc SubCmd) (*command.Command, error) {
	args, err := combineArgs(globals, sc)
	if err != nil {
		return nil, err
	}

	return Command(ctx, repo, args...)
}

// SafeBareCmd creates a git.Command with the given args, stdin/stdout/stderr,
// and env. It validates the arguments in the command before executing.
func SafeBareCmd(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer, env []string, globals []Option, sc SubCmd) (*command.Command, error) {
	args, err := combineArgs(globals, sc)
	if err != nil {
		return nil, err
	}

	return BareCommand(ctx, stdin, stdout, stderr, env, args...)
}

// SafeStdinCmd creates a git.Command with the given args and Repository that is
// suitable for Write()ing to. It validates the arguments in the command before
// executing.
func SafeStdinCmd(ctx context.Context, repo repository.GitRepo, globals []Option, sc SubCmd) (*command.Command, error) {
	args, err := combineArgs(globals, sc)
	if err != nil {
		return nil, err
	}

	return StdinCommand(ctx, repo, args...)
}

// SafeCmdWithoutRepo works like Command but without a git repository. It
// validates the arugments in the command before executing.
func SafeCmdWithoutRepo(ctx context.Context, globals []Option, sc SubCmd) (*command.Command, error) {
	args, err := combineArgs(globals, sc)
	if err != nil {
		return nil, err
	}

	return CommandWithoutRepo(ctx, args...)
}

func combineArgs(globals []Option, sc SubCmd) (_ []string, err error) {
	defer func() {
		if err != nil && IsInvalidArgErr(err) {
			incrInvalidArg(sc.Name)
		}
	}()

	var args []string

	for _, g := range globals {
		gargs, err := g.ValidateArgs()
		if err != nil {
			return nil, err
		}
		args = append(args, gargs...)
	}

	scArgs, err := sc.ValidateArgs()
	if err != nil {
		return nil, err
	}

	return append(args, scArgs...), nil
}
