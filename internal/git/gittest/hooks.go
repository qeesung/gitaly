package gittest

import (
	"fmt"
	"path/filepath"
	"testing"

	"gitlab.com/gitlab-org/gitaly/v15/internal/git"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
)

// WriteEnvToCustomHook dumps the env vars that the custom hooks receives to a file
func WriteEnvToCustomHook(tb testing.TB, repoPath, hookName string) string {
	tempDir := testhelper.TempDir(tb)

	outputPath := filepath.Join(tempDir, "hook.env")
	hookContent := fmt.Sprintf("#!/bin/sh\n/usr/bin/env >%s\n", outputPath)

	WriteCustomHook(tb, repoPath, hookName, []byte(hookContent))

	return outputPath
}

// WriteCheckNewObjectExistsHook writes a pre-receive hook which only succeeds
// if it can find the object in the quarantine directory. if
// GIT_OBJECT_DIRECTORY and GIT_ALTERNATE_OBJECT_DIRECTORIES were not passed
// through correctly to the hooks, it will fail
func WriteCheckNewObjectExistsHook(tb testing.TB, repoPath string) {
	WriteCustomHook(tb, repoPath, "pre-receive", []byte(
		`#!/bin/sh
		while read oldrev newrev reference
		do
			git cat-file -e "$newrev" || exit 1
		done
	`))
}

// WriteCustomHook writes a hook in the repo/path.git/custom_hooks directory
func WriteCustomHook(tb testing.TB, repoPath, name string, content []byte) string {
	fullPath := filepath.Join(repoPath, "custom_hooks", name)
	testhelper.WriteExecutable(tb, fullPath, content)

	return fullPath
}

// CaptureHookEnv creates a Git command factory which injects a bogus 'update' Git hook to sniff out
// what environment variables get set for hooks.
func CaptureHookEnv(tb testing.TB, cfg config.Cfg) (git.CommandFactory, string) {
	hooksPath := testhelper.TempDir(tb)
	hookOutputFile := filepath.Join(hooksPath, "hook.env")

	testhelper.WriteExecutable(tb, filepath.Join(hooksPath, "update"), []byte(fmt.Sprintf(
		`#!/usr/bin/env bash
		env | grep -e ^GIT -e ^GL_ >%q
	`, hookOutputFile)))

	return NewCommandFactory(tb, cfg, git.WithHooksPath(hooksPath)), hookOutputFile
}
