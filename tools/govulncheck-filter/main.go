package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
)

var (
	vulnIDRegex = regexp.MustCompile("^Vulnerability #[0-9]{0,3}: (GO-[0-9]{4}-[0-9]{4})")

	// If the `vulnerability` CI job fails due to an issue that the Gitaly team cannot directly patch, and the
	// vulnerability is deemed non-critical, an exception can be added to the ignore list below. Please create a
	// **confidential** issue using the "Ignored Vulnerability" template on gitlab-org/gitaly before adding a new
	// entry here.
	defaultIgnoreList = ignoreList{
		"GO-2024-2887": {
			GitLabIssueURL: "https://gitlab.com/gitlab-org/gitaly/-/issues/6116",
		},
		"GO-2024-2963": {
			GitLabIssueURL: "https://gitlab.com/gitlab-org/gitaly/-/issues/6185",
		},
		"GO-2024-2918": {
			GitLabIssueURL: "https://gitlab.com/gitlab-org/gitaly/-/issues/6186",
		},
	}

	outputPrologue = `
⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️
⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️
This check may have undergone filtering. The pipeline may be passing despite known vulnerabilities. See
./tools/govulnchecker-filter/main.go for the filtering rules.
⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️
⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️ ⚠️

The following vulnerabilities were ignored if present:
`
)

type ignoreList map[string]struct {
	GitLabIssueURL string
}

// FilteredVulncheck parses the human-readable output of the `govulncheck` tool and compares its list of affected
// vulnerabilities with an ignore list. An affected vulnerability is code which is imported/used by Gitaly, not code
// which is imported but never used, or code imported transitively by package dependencies which are never used.
// We parse the human-readable output instead of calling `govulncheck -json` because the latter does not provide a
// filtered view of vulnerabilities.
//
// IFF the set of ignored vulnerabilities is equal to the set of affected vulnerabilities, the tool returns an exit
// code of 0. Regardless of error, the original output from `govulncheck` is streamed to w.
func FilteredVulncheck(r io.Reader, w io.Writer, ignoreList ignoreList) (exitCode int, err error) {
	looking := true

	actualVulns := make(map[string]struct{})

	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		text := scanner.Text()

		_, _ = io.WriteString(w, text)
		_, _ = io.WriteString(w, "\n")

		// Keep capturing the original output, but skip further regex checking.
		if !looking {
			continue
		}

		if strings.HasPrefix(text, "=== Informational ===") {
			// The rest of the output contains informational vulnerabilities and can be ignored.
			looking = false
			continue
		}

		matches := vulnIDRegex.FindStringSubmatch(text)
		if len(matches) == 2 {
			// matches contains the entire match and the group
			actualVulns[matches[1]] = struct{}{}
		}
	}

	if scanner.Err() != nil {
		return exitCode, fmt.Errorf("read line: %w", scanner.Err())
	}

	for actualID := range actualVulns {
		if _, ok := ignoreList[actualID]; !ok {
			exitCode = 1
		}
	}

	_, _ = io.WriteString(w, outputPrologue)
	for id := range ignoreList {
		_, _ = fmt.Fprintf(w, "- %s\n", id)
	}

	return exitCode, nil
}

func main() {
	exitCode, err := FilteredVulncheck(os.Stdin, os.Stdout, defaultIgnoreList)
	if err != nil {
		panic(fmt.Errorf("filter govulncheck: %w", err))
	}

	os.Exit(exitCode)
}
