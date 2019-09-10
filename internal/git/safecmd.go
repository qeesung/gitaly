package git

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"gitlab.com/gitlab-org/gitaly/internal/command"
	"gitlab.com/gitlab-org/gitaly/internal/git/repository"
)

// SubCmd represents a specific git command
type SubCmd struct {
	Name        string
	Options     []Option
	Args        []string
	PostSepArgs []string
}

var subCmdNameRegex = regexp.MustCompile(`[[:alnum:]]+(-[[:alnum:]]+)*$`)

// ValidateArgs checks all arguments in the sub command and validates them
func (sc SubCmd) ValidateArgs() ([]string, error) {
	var safeArgs []string

	if !subCmdNameRegex.MatchString(sc.Name) {
		return nil, &invalidArgErr{
			msg: fmt.Sprintf("invalid sub command name %q", sc.Name),
		}
	}
	safeArgs = append(safeArgs, sc.Name)

	for _, o := range sc.Options {
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

// Option is a git command line option with validation logic
type Option interface {
	ValidateArgs() ([]string, error)
}

// Option1 is a single token optional command line argument that enables or
// disables functionality
type Option1 struct {
	Flag string
}

// ValidateArgs returns an error if the option is not sanitary
func (o1 Option1) ValidateArgs() ([]string, error) {
	if err := validateFlag(o1.Flag); err != nil {
		return nil, err
	}
	return []string{o1.Flag}, nil
}

// Option2 is an optional command line argument that is comprised of pair of
// tokens
type Option2 struct {
	Key   string
	Value string
}

// ValidateArgs returns an error if the option is not sanitary
func (o2 Option2) ValidateArgs() ([]string, error) {
	if err := validateFlag(o2.Key); err != nil {
		return nil, err
	}

	if o2.Value == "" {
		return nil, &invalidArgErr{
			msg: fmt.Sprintf("option value arg for %q cannot be empty", o2.Key),
		}
	}

	return []string{o2.Key, o2.Value}, nil
}

// flagRegex makes sure that a flag follows these rules:
// 1. Can be a short flag (e.g. "-L" or "-a")
// 2. Can be a long flag (e.g. "--long-flag")
// 2.1 Long flags cannot end with a dash. Interior dashes cannot repeat more
//     than once consecutively.
var flagRegex = regexp.MustCompile(
	`^((-[[:alnum:]])|(--[[:alnum:]]+(-[[:alnum:]]+)*))$`,
)

type invalidArgErr struct {
	msg string
}

func (iae invalidArgErr) Error() string { return iae.msg }

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

// SafeCmd validates the arguments in the command before executing
func SafeCmd(ctx context.Context, repo repository.GitRepo, globals []Option, sc SubCmd) (*command.Command, error) {
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

	args = append(args, scArgs...)

	return Command(ctx, repo, args...)
}
