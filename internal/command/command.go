package command

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/grpc-ecosystem/go-grpc-middleware/logging/logrus/ctxlogrus"
	grpcmwtags "github.com/grpc-ecosystem/go-grpc-middleware/tags"
	"github.com/opentracing/opentracing-go"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/sirupsen/logrus"
	"gitlab.com/gitlab-org/gitaly/v16/internal/command/commandcounter"
	"gitlab.com/gitlab-org/gitaly/v16/internal/featureflag"
	"gitlab.com/gitlab-org/gitaly/v16/internal/log"
	"gitlab.com/gitlab-org/gitaly/v16/internal/tracing"
	labkittracing "gitlab.com/gitlab-org/labkit/tracing"
)

var (
	cpuSecondsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gitaly_command_cpu_seconds_total",
			Help: "Sum of CPU time spent by shelling out",
		},
		[]string{"grpc_service", "grpc_method", "cmd", "subcmd", "mode", "git_version"},
	)
	realSecondsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gitaly_command_real_seconds_total",
			Help: "Sum of real time spent by shelling out",
		},
		[]string{"grpc_service", "grpc_method", "cmd", "subcmd", "git_version"},
	)
	minorPageFaultsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gitaly_command_minor_page_faults_total",
			Help: "Sum of minor page faults performed while shelling out",
		},
		[]string{"grpc_service", "grpc_method", "cmd", "subcmd", "git_version"},
	)
	majorPageFaultsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gitaly_command_major_page_faults_total",
			Help: "Sum of major page faults performed while shelling out",
		},
		[]string{"grpc_service", "grpc_method", "cmd", "subcmd", "git_version"},
	)
	signalsReceivedTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gitaly_command_signals_received_total",
			Help: "Sum of signals received while shelling out",
		},
		[]string{"grpc_service", "grpc_method", "cmd", "subcmd", "git_version"},
	)
	contextSwitchesTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gitaly_command_context_switches_total",
			Help: "Sum of context switches performed while shelling out",
		},
		[]string{"grpc_service", "grpc_method", "cmd", "subcmd", "ctxswitchtype", "git_version"},
	)

	// exportedEnvVars contains a list of environment variables
	// that are always exported to child processes on spawn
	exportedEnvVars = []string{
		"HOME",
		"PATH",
		"LD_LIBRARY_PATH",
		"TZ",

		// Export git tracing variables for easier debugging
		"GIT_TRACE",
		"GIT_TRACE_PACK_ACCESS",
		"GIT_TRACE_PACKET",
		"GIT_TRACE_PERFORMANCE",
		"GIT_TRACE_SETUP",

		// GIT_EXEC_PATH tells Git where to find its binaries. This must be exported
		// especially in the case where we use bundled Git executables given that we cannot
		// rely on a complete Git installation in that case.
		"GIT_EXEC_PATH",

		// Git HTTP proxy settings:
		// https://git-scm.com/docs/git-config#git-config-httpproxy
		"all_proxy",
		"http_proxy",
		"HTTP_PROXY",
		"https_proxy",
		"HTTPS_PROXY",
		// libcurl settings: https://curl.haxx.se/libcurl/c/CURLOPT_NOPROXY.html
		"no_proxy",
		"NO_PROXY",

		// We must export this variable to child processes or otherwise we end up in
		// an inconsistent state, where the parent process has all feature flags
		// force-enabled while the child is using the usual defaults.
		featureflag.EnableAllFeatureFlagsEnvVar,
	}

	// envInjector is responsible for injecting environment variables required for tracing into
	// the child process.
	envInjector = labkittracing.NewEnvInjector()

	// globalSpawnTokenManager is responsible for limiting the total number of commands that can spawn at a time in a
	// Gitaly process.
	globalSpawnTokenManager *SpawnTokenManager
)

func init() {
	var err error
	globalSpawnTokenManager, err = NewSpawnTokenManagerFromEnv()
	if err != nil {
		panic(err)
	}
	prometheus.MustRegister(globalSpawnTokenManager)
}

const (
	// maxStderrBytes is at most how many bytes will be written to stderr
	maxStderrBytes = 10000 // 10kb
	// maxStderrLineLength is at most how many bytes a single line will be
	// written to stderr. Lines exceeding this limit should be truncated
	maxStderrLineLength = 4096
)

// Command encapsulates a running exec.Cmd. The embedded exec.Cmd is
// terminated and reaped automatically when the context.Context that
// created it is canceled.
type Command struct {
	reader       atomic.Pointer[io.ReadCloser]
	writer       io.WriteCloser
	stderrBuffer *stderrBuffer
	cmd          *exec.Cmd
	context      context.Context
	startTime    time.Time

	waitError       error
	waitOnce        sync.Once
	processExitedCh chan struct{}

	finalizers []func(context.Context, *Command)

	span opentracing.Span

	metricsCmd    string
	metricsSubCmd string
	cgroupPath    string
	cmdGitVersion string
}

// New creates a Command from the given executable name and arguments On success, the Command
// contains a running subprocess. When ctx is canceled the embedded process will be terminated and
// reaped automatically.
func New(ctx context.Context, nameAndArgs []string, opts ...Option) (*Command, error) {
	if ctx.Done() == nil {
		panic("command spawned with context without Done() channel")
	}

	// Don't launch the command if the context is already canceled. This matches
	// Go's own CommandContext's behavior which doesn't start a command if the
	// context is already canceled. We can't use it currently due to it sending a
	// SIGKILL to the command when the context is canceled during execution. This is not
	// fine as we don't have proper logic to recover from git crashes by for example
	// cleaning stale reference locks.
	//
	// Without this, racy behavior will emerge as the command execution will race with the
	// context cancellation. This really only helps with cases when the context is already
	// canceled before calling New. Raciness still ensues if the context is canceled after
	// this check. More details at: https://gitlab.com/gitlab-org/gitaly/-/issues/5021
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if len(nameAndArgs) == 0 {
		panic("command spawned without name")
	}

	if err := checkNullArgv(nameAndArgs); err != nil {
		return nil, err
	}

	var cfg config
	for _, opt := range opts {
		opt(&cfg)
	}

	spawnTokenManager := cfg.spawnTokenManager
	if spawnTokenManager == nil {
		spawnTokenManager = globalSpawnTokenManager
	}
	putToken, err := spawnTokenManager.GetSpawnToken(ctx)
	if err != nil {
		return nil, err
	}
	cmdName := path.Base(nameAndArgs[0])
	defer putToken()

	logPid := -1
	defer func() {
		ctxlogrus.Extract(ctx).WithFields(logrus.Fields{
			"pid":  logPid,
			"path": nameAndArgs[0],
			"args": nameAndArgs[1:],
		}).Debug("spawn")
	}()

	var spanName string
	if cfg.commandName != "" && cfg.subcommandName != "" {
		spanName = fmt.Sprintf("%s-%s", cfg.commandName, cfg.subcommandName)
	} else {
		spanName = cmdName
	}
	span, ctx := tracing.StartSpanIfHasParent(
		ctx,
		spanName,
		tracing.Tags{
			"path": nameAndArgs[0],
			"args": strings.Join(nameAndArgs[1:], " "),
		},
	)
	cmd := exec.Command(nameAndArgs[0], nameAndArgs[1:]...)

	command := &Command{
		cmd:             cmd,
		startTime:       time.Now(),
		context:         ctx,
		span:            span,
		finalizers:      cfg.finalizers,
		metricsCmd:      cfg.commandName,
		metricsSubCmd:   cfg.subcommandName,
		cmdGitVersion:   cfg.gitVersion,
		processExitedCh: make(chan struct{}),
	}

	cmd.Dir = cfg.dir

	// Export allowed environment variables as set in the Gitaly process.
	cmd.Env = AllowedEnvironment(os.Environ())
	// Append environment variables explicitly requested by the caller.
	cmd.Env = append(cmd.Env, cfg.environment...)
	// And finally inject environment variables required for tracing into the command.
	cmd.Env = envInjector(ctx, cmd.Env)

	// Start the command in its own process group (nice for signalling)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	// Three possible values for stdin:
	//   * nil - Go implicitly uses /dev/null
	//   * stdinSentinel - configure with cmd.StdinPipe(), allowing Write() to work
	//   * Another io.Reader - becomes cmd.Stdin. Write() will not work
	if _, ok := cfg.stdin.(stdinSentinel); ok {
		pipe, err := cmd.StdinPipe()
		if err != nil {
			return nil, fmt.Errorf("creating stdin pipe: %w", err)
		}

		command.writer = pipe
	} else if cfg.stdin != nil {
		cmd.Stdin = cfg.stdin
	}

	if cfg.stdout != nil {
		// We don't assign a reader if an stdout override was passed. We assume
		// output is going to be directly handled by the caller.
		cmd.Stdout = cfg.stdout
	} else {
		pipe, err := cmd.StdoutPipe()
		if err != nil {
			return nil, fmt.Errorf("creating stdout pipe: %w", err)
		}

		command.reader.Store(&pipe)
	}

	if cfg.stderr != nil {
		cmd.Stderr = cfg.stderr
	} else {
		command.stderrBuffer, err = newStderrBuffer(maxStderrBytes, maxStderrLineLength, []byte("\n"))
		if err != nil {
			return nil, fmt.Errorf("creating stderr buffer: %w", err)
		}

		cmd.Stderr = command.stderrBuffer
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting process %v: %w", cmd.Args, err)
	}

	inFlightCommandGauge.Inc()
	commandcounter.Increment()

	// The goroutine below is responsible for terminating and reaping the process when ctx is
	// canceled. While we must ensure that it does run when `cmd.Start()` was successful, it
	// must not run before have fully set up the command. Otherwise, we may end up with racy
	// access patterns when the context gets terminated early.
	//
	// We thus defer spawning the Goroutine.
	defer func() {
		go func() {
			select {
			case <-ctx.Done():
				// If the context has been cancelled and we didn't explicitly reap
				// the child process then we need to manually kill it and release
				// all associated resources.
				if cmd.Process.Pid > 0 {
					//nolint:errcheck // TODO: do we want to report errors?
					// Send SIGTERM to the process group of cmd
					syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
				}

				// We do not care for any potential error code, but just want to
				// make sure that the subprocess gets properly killed and processed.
				_ = command.Wait()
			case <-command.processExitedCh:
				// Otherwise, if the process has exited via a call to `wait()`
				// already then there is nothing we need to do.
			}
		}()
	}()

	if cfg.cgroupsManager != nil {
		cgroupPath, err := cfg.cgroupsManager.AddCommand(command.cmd, cfg.cgroupsAddCommandOpts...)
		if err != nil {
			return nil, err
		}

		command.cgroupPath = cgroupPath
	}

	logPid = cmd.Process.Pid

	return command, nil
}

// Read calls Read() on the stdout pipe of the command or temporary buffer
// that contains max 1024 bytes of the output in case the command execution
// context is cancelled.
func (c *Command) Read(p []byte) (int, error) {
	readerPtr := c.reader.Load()
	if readerPtr == nil || *readerPtr == nil {
		panic("command has no reader")
	}

	return (*readerPtr).Read(p)
}

// Write calls Write() on the stdin pipe of the command.
func (c *Command) Write(p []byte) (int, error) {
	if c.writer == nil {
		panic("command has no writer")
	}

	return c.writer.Write(p)
}

// Wait calls Wait() on the exec.Cmd instance inside the command. This
// blocks until the command has finished and reports the command exit
// status via the error return value. Use ExitStatus to get the integer
// exit status from the error returned by Wait().
//
// Wait returns a wrapped context error if the process was reaped due to
// the context being done.
func (c *Command) Wait() error {
	c.waitOnce.Do(c.wait)

	return c.waitError
}

// This function should never be called directly, use Wait().
func (c *Command) wait() {
	defer close(c.processExitedCh)

	if c.writer != nil {
		// Prevent the command from blocking on waiting for stdin to be closed
		c.writer.Close()
	}

	contextIsDone := c.context.Err() != nil

	if readerPtr := c.reader.Load(); readerPtr != nil && *readerPtr != nil {
		if contextIsDone {
			// When context is done it is not yet mean the running command should fail.
			// If the termination error is not yet processed by the command it may produce
			// useful output that can be processed by the caller. That is why we consume
			// max up to 1024 bytes from it and discard all the other info because of
			// safety reasons. The consumed data can be read from the buffer by another
			// goroutine. All remaining output of the command will be discarded.
			buffer := &bytes.Buffer{}
			_, _ = io.Copy(buffer, io.LimitReader(*readerPtr, 1024))
			tmpBuffer := io.NopCloser(buffer)
			c.reader.Store(&tmpBuffer)
		}
		// Prevent the command from blocking on writing to its stdout.
		_, _ = io.Copy(io.Discard, *readerPtr)
	}

	c.waitError = c.cmd.Wait()

	// If the context is done, the process was likely terminated due to it. If so,
	// we return the context error to correctly report the reason.
	if contextIsDone {
		// The standard library sets exit status -1 if the process was terminated by a signal,
		// such as the SIGTERM sent when context is done.
		if exitCode, ok := ExitStatus(c.waitError); ok && exitCode == -1 {
			//nolint:gitaly-linters // We can only wrap one
			c.waitError = fmt.Errorf("%s: %w", c.waitError, c.context.Err())
		}
	}

	inFlightCommandGauge.Dec()

	c.logProcessComplete()

	// This is a bit out-of-place here given that the `commandcounter.Increment()` call is in
	// `New()`. But in `New()`, we have to resort waiting on the context being finished until we
	// would be able to decrement the number of in-flight commands. Given that in some
	// cases we detach processes from their initial context such that they run in the
	// background, this would cause us to take longer than necessary to decrease the wait group
	// counter again. So we instead do it here to accelerate the process, even though it's less
	// idiomatic.
	commandcounter.Decrement()

	for _, finalizer := range c.finalizers {
		finalizer(c.context, c)
	}
}

func (c *Command) logProcessComplete() {
	exitCode := 0
	if c.waitError != nil {
		if exitStatus, ok := ExitStatus(c.waitError); ok {
			exitCode = exitStatus
		}
	}

	ctx := c.context
	cmd := c.cmd

	systemTime := cmd.ProcessState.SystemTime()
	userTime := cmd.ProcessState.UserTime()
	realTime := time.Since(c.startTime)

	fields := logrus.Fields{
		"pid":                    cmd.ProcessState.Pid(),
		"path":                   cmd.Path,
		"args":                   cmd.Args,
		"command.exitCode":       exitCode,
		"command.system_time_ms": systemTime.Milliseconds(),
		"command.user_time_ms":   userTime.Milliseconds(),
		"command.cpu_time_ms":    systemTime.Milliseconds() + userTime.Milliseconds(),
		"command.real_time_ms":   realTime.Milliseconds(),
	}

	if c.cgroupPath != "" {
		fields["command.cgroup_path"] = c.cgroupPath
	}

	entry := ctxlogrus.Extract(ctx).WithFields(fields)

	rusage, ok := cmd.ProcessState.SysUsage().(*syscall.Rusage)
	if ok {
		entry = entry.WithFields(logrus.Fields{
			"command.maxrss":  rusage.Maxrss,
			"command.inblock": rusage.Inblock,
			"command.oublock": rusage.Oublock,
		})
	}

	entry.Debug("spawn complete")
	if c.stderrBuffer != nil && c.stderrBuffer.Len() > 0 {
		entry.Error(c.stderrBuffer.String())
	}

	if customFields := log.CustomFieldsFromContext(ctx); customFields != nil {
		customFields.RecordSum("command.count", 1)
		customFields.RecordSum("command.system_time_ms", int(systemTime.Milliseconds()))
		customFields.RecordSum("command.user_time_ms", int(userTime.Milliseconds()))
		customFields.RecordSum("command.cpu_time_ms", int(systemTime.Milliseconds()+userTime.Milliseconds()))
		customFields.RecordSum("command.real_time_ms", int(realTime.Milliseconds()))

		if ok {
			customFields.RecordMax("command.maxrss", int(rusage.Maxrss))
			customFields.RecordSum("command.inblock", int(rusage.Inblock))
			customFields.RecordSum("command.oublock", int(rusage.Oublock))
			customFields.RecordSum("command.minflt", int(rusage.Minflt))
			customFields.RecordSum("command.majflt", int(rusage.Majflt))
		}

		if c.cgroupPath != "" {
			customFields.RecordMetadata("command.cgroup_path", c.cgroupPath)
		}
	}

	service, method := methodFromContext(ctx)
	cmdName := path.Base(c.cmd.Path)
	if c.metricsCmd != "" {
		cmdName = c.metricsCmd
	}
	cpuSecondsTotal.WithLabelValues(service, method, cmdName, c.metricsSubCmd, "system", c.cmdGitVersion).Add(systemTime.Seconds())
	cpuSecondsTotal.WithLabelValues(service, method, cmdName, c.metricsSubCmd, "user", c.cmdGitVersion).Add(userTime.Seconds())
	realSecondsTotal.WithLabelValues(service, method, cmdName, c.metricsSubCmd, c.cmdGitVersion).Add(realTime.Seconds())
	if ok {
		minorPageFaultsTotal.WithLabelValues(service, method, cmdName, c.metricsSubCmd, c.cmdGitVersion).Add(float64(rusage.Minflt))
		majorPageFaultsTotal.WithLabelValues(service, method, cmdName, c.metricsSubCmd, c.cmdGitVersion).Add(float64(rusage.Majflt))
		signalsReceivedTotal.WithLabelValues(service, method, cmdName, c.metricsSubCmd, c.cmdGitVersion).Add(float64(rusage.Nsignals))
		contextSwitchesTotal.WithLabelValues(service, method, cmdName, c.metricsSubCmd, "voluntary", c.cmdGitVersion).Add(float64(rusage.Nvcsw))
		contextSwitchesTotal.WithLabelValues(service, method, cmdName, c.metricsSubCmd, "nonvoluntary", c.cmdGitVersion).Add(float64(rusage.Nivcsw))
	}

	c.span.SetTag("pid", cmd.ProcessState.Pid())
	c.span.SetTag("exit_code", exitCode)
	c.span.SetTag("system_time_ms", systemTime.Milliseconds())
	c.span.SetTag("user_time_ms", userTime.Milliseconds())
	c.span.SetTag("real_time_ms", realTime.Milliseconds())
	if ok {
		c.span.SetTag("maxrss", rusage.Maxrss)
		c.span.SetTag("inblock", rusage.Inblock)
		c.span.SetTag("oublock", rusage.Oublock)
		c.span.SetTag("minflt", rusage.Minflt)
		c.span.SetTag("majflt", rusage.Majflt)
	}
	if c.cgroupPath != "" {
		c.span.SetTag("cgroup_path", c.cgroupPath)
	}
	c.span.Finish()
}

// Args is an accessor for the command arguments
func (c *Command) Args() []string {
	return c.cmd.Args
}

// Env is an accessor for the environment variables
func (c *Command) Env() []string {
	return c.cmd.Env
}

// Pid is an accessor for the pid
func (c *Command) Pid() int {
	return c.cmd.Process.Pid
}

type stdinSentinel struct{}

func (stdinSentinel) Read([]byte) (int, error) {
	return 0, errors.New("stdin sentinel should not be read from")
}

// AllowedEnvironment filters the given slice of environment variables and
// returns all variables which are allowed per the variables defined above.
// This is useful for constructing a base environment in which a command can be
// run.
func AllowedEnvironment(envs []string) []string {
	var filtered []string

	for _, env := range envs {
		for _, exportedEnv := range exportedEnvVars {
			if strings.HasPrefix(env, exportedEnv+"=") {
				filtered = append(filtered, env)
			}
		}
	}

	return filtered
}

// ExitStatus will return the exit-code from an error returned by Wait().
func ExitStatus(err error) (int, bool) {
	exitError, ok := err.(*exec.ExitError)
	if !ok {
		return 0, false
	}

	return exitError.ExitCode(), true
}

func methodFromContext(ctx context.Context) (service string, method string) {
	tags := grpcmwtags.Extract(ctx)
	ctxValue := tags.Values()["grpc.request.fullMethod"]
	if ctxValue == nil {
		return "", ""
	}

	if s, ok := ctxValue.(string); ok {
		// Expect: "/foo.BarService/Qux"
		split := strings.Split(s, "/")
		if len(split) != 3 {
			return "", ""
		}

		return split[1], split[2]
	}

	return "", ""
}

// Command arguments will be passed to the exec syscall as null-terminated C strings. That means the
// arguments themselves may not contain a null byte. The go stdlib checks for null bytes but it
// returns a cryptic error. This function returns a more explicit error.
func checkNullArgv(args []string) error {
	for _, arg := range args {
		if strings.IndexByte(arg, 0) > -1 {
			// Use %q so that the null byte gets printed as \x00
			return fmt.Errorf("detected null byte in command argument %q", arg)
		}
	}

	return nil
}
