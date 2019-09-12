package git

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"

	"gitlab.com/gitlab-org/gitaly/internal/command"
	"gitlab.com/gitlab-org/gitaly/internal/git/repository"
)

// SubCmd represents a specific git command
type SubCmd struct {
	Name        string
	Flags       []Flag
	Args        []string
	PostSepArgs []string
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

	for _, a := range sc.PostSepArgs {
		if err := validatePositionalArg(a); err != nil {
			return nil, err
		}
		safeArgs = append(safeArgs, a)
	}

	return safeArgs, nil
}

// Flag is a git command line flag with validation logic
type Flag interface {
	IsFlag()
	ValidateArgs() ([]string, error)
}

// Flag1 is a single token optional command line argument that enables or
// disables functionality (e.g. "-L")
type Flag1 struct {
	Name string
}

// IsFlag is a method present on all Flag interface implementations
func (Flag1) IsFlag() {}

// ValidateArgs returns an error if the flag is not sanitary
func (f1 Flag1) ValidateArgs() ([]string, error) {
	if err := validateFlag(f1.Name); err != nil {
		return nil, err
	}
	return []string{f1.Name}, nil
}

// Flag2 is an optional command line argument that is comprised of pair of
// tokens (e.g. "-n 50")
type Flag2 struct {
	Name  string
	Value string
}

// IsFlag is a method present on all Flag interface implementations
func (Flag2) IsFlag() {}

// ValidateArgs returns an error if the flag is not sanitary
func (f2 Flag2) ValidateArgs() ([]string, error) {
	if err := validateFlag(f2.Name); err != nil {
		return nil, err
	}

	if f2.Value == "" {
		return nil, &invalidArgErr{
			msg: fmt.Sprintf("flag value arg for %q cannot be empty", f2.Name),
		}
	}

	return []string{f2.Name, f2.Value}, nil
}

// flagRegex makes sure that a flag follows these rules:
// 1. Can be a short flag (e.g. "-L" or "-a")
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

// IsFlag is a method present on all Flag interface implementations
func (FlagCombo) IsFlag() {}

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

	if arg == "" {
		return &invalidArgErr{
			msg: fmt.Sprintf("argument cannot be blank"),
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
func SafeCmd(ctx context.Context, repo repository.GitRepo, globals []Flag, sc SubCmd) (*command.Command, error) {
	args, err := combineArgs(globals, sc)
	if err != nil {
		return nil, err
	}

	return Command(ctx, repo, args...)
}

// SafeBareCmd creates a git.Command with the given args, stdin/stdout/stderr,
// and env. It validates the arguments in the command before executing.
func SafeBareCmd(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer, env []string, globals []Flag, sc SubCmd) (*command.Command, error) {
	args, err := combineArgs(globals, sc)
	if err != nil {
		return nil, err
	}

	return BareCommand(ctx, stdin, stdout, stderr, env, args...)
}

// SafeStdinCmd creates a git.Command with the given args and Repository that is
// suitable for Write()ing to. It validates the arguments in the command before
// executing.
func SafeStdinCmd(ctx context.Context, repo repository.GitRepo, globals []Flag, sc SubCmd) (*command.Command, error) {
	args, err := combineArgs(globals, sc)
	if err != nil {
		return nil, err
	}

	return StdinCommand(ctx, repo, args...)
}

// SafeCmdWithoutRepo works like Command but without a git repository. It
// validates the arugments in the command before executing.
func SafeCmdWithoutRepo(ctx context.Context, globals []Flag, sc SubCmd) (*command.Command, error) {
	args, err := combineArgs(globals, sc)
	if err != nil {
		return nil, err
	}

	return CommandWithoutRepo(ctx, args...)
}

func combineArgs(globals []Flag, sc SubCmd) ([]string, error) {
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
