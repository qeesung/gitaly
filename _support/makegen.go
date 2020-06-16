/*
	makegen.go -- Makefile generator for Gitaly

This file is used to generate _build/Makefile. In _build/Makefile we
can assume that we are in a GOPATH (rooted at _build) and that
$GOPATH/bin is in PATH. The generator runs in the root of the Gitaly
tree. The goal of the generator is to use as little dynamic behaviors
in _build/Makefile (e.g. shelling out to find a list of files), and do
these things as much as possible in Go and then pass them into the
template.

The working directory of makegen.go and the Makefile it generates is
_build.
*/

package main

import (
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"text/template"
	"time"
)

func main() {
	gm := &gitalyMake{}

	tmpl := template.New("Makefile")
	tmpl.Funcs(map[string]interface{}{
		"join": strings.Join,
	})
	tmpl = template.Must(tmpl.Parse(templateText))

	err := tmpl.Execute(os.Stdout, gm)
	if err != nil {
		log.Fatalf("execution failed: %s", err)
	}
}

type gitalyMake struct {
	commandPackages []string
	cwd             string
	versionPrefixed string
	goFiles         []string
	buildTime       string
}

// BuildDir is the GOPATH root. It is also the working directory of the Makefile we are generating.
func (gm *gitalyMake) BuildDir() string {
	if len(gm.cwd) > 0 {
		return gm.cwd
	}

	cwd, err := os.Getwd()
	if err != nil {
		log.Fatal(err)
	}
	gm.cwd, err = filepath.EvalSymlinks(cwd)
	if err != nil {
		log.Fatal(err)
	}

	return gm.cwd
}

func (gm *gitalyMake) Pkg() string         { return "gitlab.com/gitlab-org/gitaly" }
func (gm *gitalyMake) GoImports() string   { return "bin/goimports" }
func (gm *gitalyMake) GitalyFmt() string   { return filepath.Join(gm.BuildDir(), "bin/gitalyfmt") }
func (gm *gitalyMake) GoLint() string      { return filepath.Join(gm.BuildDir(), "bin/golangci-lint") }
func (gm *gitalyMake) GoLicenses() string  { return "bin/go-licenses" }
func (gm *gitalyMake) StaticCheck() string { return filepath.Join(gm.BuildDir(), "bin/staticcheck") }
func (gm *gitalyMake) ProtoC() string      { return filepath.Join(gm.BuildDir(), "protoc/bin/protoc") }
func (gm *gitalyMake) ProtoCGenGo() string { return filepath.Join(gm.BuildDir(), "bin/protoc-gen-go") }
func (gm *gitalyMake) GoJunitReport() string {
	return filepath.Join(gm.BuildDir(), "bin/go-junit-report")
}
func (gm *gitalyMake) ProtoCGenGitaly() string {
	return filepath.Join(gm.BuildDir(), "bin/protoc-gen-gitaly")
}
func (gm *gitalyMake) GrpcToolsRuby() string {
	return filepath.Join(gm.BuildDir(), "bin/grpc_tools_ruby_protoc")
}
func (gm *gitalyMake) CoverageDir() string       { return filepath.Join(gm.BuildDir(), "cover") }
func (gm *gitalyMake) GitalyRubyDir() string     { return filepath.Join(gm.SourceDir(), "ruby") }
func (gm *gitalyMake) GitlabShellRelDir() string { return "ruby/gitlab-shell" }
func (gm *gitalyMake) GitlabShellDir() string {
	return filepath.Join(gm.SourceDir(), gm.GitlabShellRelDir())
}

func (gm *gitalyMake) SourceDir() string {
	return os.Getenv("SOURCE_DIR")
}

func (gm *gitalyMake) TestRepoStoragePath() string {
	return filepath.Join(gm.SourceDir(), "internal/testhelper/testdata/data")
}

func (gm *gitalyMake) TestRepo() string {
	return filepath.Join(gm.TestRepoStoragePath(), "gitlab-test.git")
}

func (gm *gitalyMake) GitTestRepo() string {
	return filepath.Join(gm.TestRepoStoragePath(), "gitlab-git-test.git")
}

func (gm *gitalyMake) MakegenDep() string {
	return strings.Join([]string{
		filepath.Join(gm.SourceDir(), "_support/makegen.go"),
		filepath.Join(gm.SourceDir(), "_support/Makefile.template"),
	}, " ")
}

func (gm *gitalyMake) CommandPackages() []string {
	if len(gm.commandPackages) > 0 {
		return gm.commandPackages
	}

	entries, err := ioutil.ReadDir(filepath.Join(gm.SourceDir(), "cmd"))
	if err != nil {
		log.Fatal(err)
	}

	for _, dir := range entries {
		if !dir.IsDir() {
			continue
		}

		gm.commandPackages = append(gm.commandPackages, filepath.Join(gm.Pkg(), "cmd", dir.Name()))
	}

	return gm.commandPackages
}

func (gm *gitalyMake) Commands() []string {
	var out []string
	for _, pkg := range gm.CommandPackages() {
		out = append(out, filepath.Base(pkg))
	}
	return out
}

func (gm *gitalyMake) BuildTime() string {
	if len(gm.buildTime) > 0 {
		return gm.buildTime
	}

	now := time.Now().UTC()
	gm.buildTime = fmt.Sprintf("%d%02d%02d.%02d%02d%02d", now.Year(), now.Month(), now.Day(), now.Hour(), now.Minute(), now.Second())
	return gm.buildTime
}

func (gm *gitalyMake) GoLdFlags() string {
	return fmt.Sprintf("-ldflags '-X %s/internal/version.version=%s -X %s/internal/version.buildtime=%s'", gm.Pkg(), gm.Version(), gm.Pkg(), gm.BuildTime())
}

func (gm *gitalyMake) VersionFromFile() string {
	data, err := ioutil.ReadFile("../VERSION")
	if err != nil {
		log.Printf("error obtaining version from file: %v", err)
		return ""
	}

	return fmt.Sprintf("v%s", strings.TrimSpace(string(data)))
}

func (gm *gitalyMake) VersionFromGit() string {
	cmd := exec.Command("git", "describe")
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		log.Printf("error obtaining version from git: %s: %v", strings.Join(cmd.Args, " "), err)
		return ""
	}

	return strings.TrimSpace(string(out))
}

func (gm *gitalyMake) VersionPrefixed() string {
	if len(gm.versionPrefixed) > 0 {
		return gm.versionPrefixed
	}

	version := gm.VersionFromGit()
	if version == "" {
		log.Printf("Attempting to get the version from file")
		version = gm.VersionFromFile()
	}

	if version == "" {
		version = "unknown"
	}

	gm.versionPrefixed = version
	return gm.versionPrefixed
}

func (gm *gitalyMake) Version() string { return strings.TrimPrefix(gm.VersionPrefixed(), "v") }

func (gm *gitalyMake) GoFiles() []string {
	if len(gm.goFiles) > 0 {
		return gm.goFiles
	}

	root := gm.SourceDir() + "/." // Add "/." to traverse symlink

	filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() && path != root {
			switch path {
			case filepath.Join(root, "ruby"):
				return filepath.SkipDir
			case filepath.Join(root, "vendor"):
				return filepath.SkipDir
			case filepath.Join(root, "proto/go"):
				return filepath.SkipDir
			}

			if name := info.Name(); name == "testdata" || strings.HasPrefix(name, "_") || strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}
		}

		if !info.IsDir() && strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, ".pb.go") {
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			gm.goFiles = append(gm.goFiles, rel)
		}

		return nil
	})

	sort.Strings(gm.goFiles)

	return gm.goFiles
}

func (gm *gitalyMake) AllPackages() []string {
	pkgMap := make(map[string]struct{})
	for _, f := range gm.GoFiles() {
		pkgMap[filepath.Dir(filepath.Join(gm.Pkg(), f))] = struct{}{}
	}

	var pkgs []string
	for k := range pkgMap {
		pkgs = append(pkgs, k)
	}

	sort.Strings(pkgs)

	return pkgs
}

type downloadInfo struct {
	url    string
	sha256 string
}

var protoCDownload = map[string]downloadInfo{
	"darwin/amd64": downloadInfo{
		url:    "https://github.com/protocolbuffers/protobuf/releases/download/v3.6.1/protoc-3.6.1-osx-x86_64.zip",
		sha256: "0decc6ce5beed07f8c20361ddeb5ac7666f09cf34572cca530e16814093f9c0c",
	},
	"linux/amd64": downloadInfo{
		url:    "https://github.com/protocolbuffers/protobuf/releases/download/v3.6.1/protoc-3.6.1-linux-x86_64.zip",
		sha256: "6003de742ea3fcf703cfec1cd4a3380fd143081a2eb0e559065563496af27807",
	},
}

func (gm *gitalyMake) ProtoCURL() string {
	return protoCDownload[runtime.GOOS+"/"+runtime.GOARCH].url
}

func (gm *gitalyMake) ProtoCSHA256() string {
	return protoCDownload[runtime.GOOS+"/"+runtime.GOARCH].sha256
}

func (gm *gitalyMake) MakeFormatCheck() string {
	return `awk '{ print } END { if(NR>0) { print "Formatting error, run make format"; exit(1) } }'`
}

func (gm *gitalyMake) GolangCILintURL() string {
	return "https://github.com/golangci/golangci-lint/releases/download/v" + gm.GolangCILintVersion() + "/" + gm.GolangCILint() + ".tar.gz"
}

func (gm *gitalyMake) GolangCILintSHA256() string {
	switch runtime.GOOS + "/" + runtime.GOARCH {
	case "darwin/amd64":
		return "f05af56f15ebbcf77663a8955d1e39009b584ce8ea4c5583669369d80353a113"
	case "linux/amd64":
		return "241ca454102e909de04957ff8a5754c757cefa255758b3e1fba8a4533d19d179"
	default:
		return "unknown"
	}
}

func (gm *gitalyMake) GolangCILintVersion() string {
	return "1.24.0"
}

func (gm *gitalyMake) GolangCILint() string {
	return fmt.Sprintf("golangci-lint-%s-%s-%s", gm.GolangCILintVersion(), runtime.GOOS, runtime.GOARCH)
}

func (gm *gitalyMake) BundleFlags() string {
	if gm.IsGDK() {
		return "--no-deployment"
	}

	return "--deployment"
}

func (gm *gitalyMake) IsGDK() bool {
	_, err := os.Stat(filepath.Join(gm.SourceDir(), "../.gdk-install-root"))
	return err == nil
}

// Variables used with Git

func (gm *gitalyMake) GitDefaultRev() string {
	rev := os.Getenv("GIT_REV")
	if rev != "" {
		return rev
	}

	// Gitaly defaults to a supported version for Git, which should be a
	// valid tag in: https://gitlab.com/gitlab-org/gitlab-git/-/tags
	return "v2.27.0"
}

func (gm *gitalyMake) GitDefaultBuildJob() string {
	job := os.Getenv("GIT_BUILD_JOB")
	if job != "" {
		return job
	}
	return "build"
}

func (gm *gitalyMake) GitInstallDir() string   { return filepath.Join(gm.BuildDir(), "git") }
func (gm *gitalyMake) GitBuildTarball() string { return "git_full_bins.tgz" }

func (gm *gitalyMake) GitArtifactUrl() string {
	return "https://gitlab.com/gitlab-org/gitlab-git/-/jobs/artifacts/" +
		gm.GitDefaultRev() + "/raw/" +
		gm.GitBuildTarball() + "?job=" +
		gm.GitDefaultBuildJob()
}

func (gm *gitalyMake) GitDefaultRepoUrl() string {
	url := os.Getenv("GIT_REPO_URL")
	if url != "" {
		return url
	}
	return "https://gitlab.com/gitlab-org/gitlab-git.git"
}

func (gm *gitalyMake) GitSourceDir() string { return filepath.Join(gm.BuildDir(), "src", "git") }

func (gm *gitalyMake) GitBuildOptions() string {
	buildOptions := []string{
		fmt.Sprintf("-j%v", runtime.NumCPU() + 1), // use multiple parallele jobs
		"DEVELOPER=1",                             // activate developer checks
		"CFLAGS='-O0 -g3'",                        // make it easy to debug in case of crashes
		"NO_PERL=YesPlease",
		"NO_EXPAT=YesPlease",
		"NO_TCLTK=YesPlease",
		"NO_REGEX=YesPlease",                      // fix compilation on musl libc
		"NO_GETTEXT=YesPlease",
		"NO_PYTHON=YesPlease",
		"NO_INSTALL_HARDLINKS=YesPlease",
		"NO_R_TO_GCC_LINKER=YesPlease",
	}
	return strings.Join(buildOptions, " ")
}

func (gm *gitalyMake) GitDefaultPrefix() string {
	prefix := os.Getenv("GIT_PREFIX")
	if prefix != "" {
		return prefix
	}
	return gm.GitInstallDir()
}

var templateText = func() string {
	contents, err := ioutil.ReadFile("../_support/Makefile.template")
	if err != nil {
		panic(err)
	}
	return string(contents)
}()
