// Package repository prepares a repository inside a sandbox.
//
// Two source kinds are supported:
//
//   - SourceRemote clones a git URL. The URL must pass operator validation.
//   - SourceLocal packs a repository that exists on the FACTORY HOST into a git
//     bundle, pushes the bundle into the sandbox, and clones from it. This is
//     how the end-to-end acceptance test runs entirely offline and without
//     touching any real remote repository.
//
// Packing a host repository into a bundle does not execute repository code, so
// the "the host must not run untrusted repository code" rule still holds.
//
// Nothing in this package pushes, creates a branch on a remote, or opens a pull
// request.
package repository

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path"
	"strings"
	"time"

	"github.com/mitkox/esf/internal/sandbox"
)

// Kind identifies where a repository comes from.
type Kind string

const (
	// SourceRemote clones over the network from a git URL.
	SourceRemote Kind = "remote"
	// SourceLocal packs a host-side repository into the sandbox.
	SourceLocal Kind = "local"
)

// ErrRepositoryRejected reports a repository the operator policy disallows.
var ErrRepositoryRejected = errors.New("repository: rejected by policy")

// Request identifies the repository and revision to prepare.
type Request struct {
	// Kind selects the source. Empty means SourceRemote.
	Kind Kind
	// URL is the git URL for SourceRemote.
	URL string
	// LocalPath is a host path for SourceLocal.
	LocalPath string
	// Revision is the exact revision to check out. Required: a run must be
	// reproducible, so "whatever HEAD happens to be" is not acceptable.
	Revision string
	// DestDir is the absolute in-sandbox directory to clone into.
	DestDir string
}

// Baseline records exactly what the agent started from.
type Baseline struct {
	Kind           Kind              `json:"kind"`
	Source         string            `json:"source"`
	RequestedRev   string            `json:"requested_revision"`
	SHA            string            `json:"sha"`
	Branch         string            `json:"branch,omitempty"`
	Status         string            `json:"status_porcelain"`
	LockfileHashes map[string]string `json:"lockfile_hashes,omitempty"`
	PreparedAt     time.Time         `json:"prepared_at"`
}

// Provider prepares repositories.
type Provider struct {
	allowed []string
	log     *slog.Logger
	// runHostCommand allows tests to replace host-side git invocation.
	runHostCommand func(ctx context.Context, dir string, argv ...string) (string, error)
}

// Option customizes a Provider.
type Option func(*Provider)

// WithLogger sets the structured logger.
func WithLogger(log *slog.Logger) Option {
	return func(p *Provider) {
		if log != nil {
			p.log = log
		}
	}
}

// New creates a repository provider.
//
// allowed is the operator-controlled allowlist of git URL prefixes (for
// example "https://github.com/my-org/"). It may be empty, which disallows all
// remote repositories; local repositories are unaffected.
func New(allowed []string, opts ...Option) *Provider {
	p := &Provider{
		allowed: append([]string(nil), allowed...),
		log:     slog.New(slog.DiscardHandler),
	}
	for _, opt := range opts {
		opt(p)
	}
	p.runHostCommand = runGitHost
	return p
}

// Allowed returns the configured remote allowlist.
func (p *Provider) Allowed() []string { return append([]string(nil), p.allowed...) }

// Validate checks a request against operator repository policy without touching
// a sandbox.
//
// It exists so the factory can reject a disallowed repository BEFORE any
// sandbox is created, and so the same rule is enforced at every entry point.
func (p *Provider) Validate(req Request) error {
	return req.validate(p.allowed)
}

// Prepare clones the repository into the sandbox and records the baseline.
func (p *Provider) Prepare(ctx context.Context, sb sandbox.Sandbox, req Request) (Baseline, error) {
	if err := req.validate(p.allowed); err != nil {
		return Baseline{}, err
	}
	dest := req.DestDir
	if dest == "" {
		dest = "/workspace/repository"
	}

	switch req.Kind {
	case SourceRemote:
		return p.prepareRemote(ctx, sb, req, dest)
	case SourceLocal:
		return p.prepareLocal(ctx, sb, req, dest)
	default:
		return Baseline{}, fmt.Errorf("repository: unknown source kind %q", req.Kind)
	}
}

func (p *Provider) prepareRemote(ctx context.Context, sb sandbox.Sandbox, req Request, dest string) (Baseline, error) {
	steps := []sandbox.Command{
		{Argv: []string{"mkdir", "-p", path.Dir(dest)}, Description: "create workspace"},
		{
			// --no-tags and a single branch keep the clone small and fast, which
			// matters because the base image has ~900 MiB of disk.
			Argv:        []string{"git", "clone", "--no-tags", "--recurse-submodules", req.URL, dest},
			Timeout:     10 * time.Minute,
			Description: "clone repository",
		},
	}
	if err := runAll(ctx, sb, steps); err != nil {
		return Baseline{}, err
	}

	return p.checkout(ctx, sb, Baseline{
		Kind:         SourceRemote,
		Source:       req.URL,
		RequestedRev: req.Revision,
	}, dest)
}

func (p *Provider) prepareLocal(ctx context.Context, sb sandbox.Sandbox, req Request, dest string) (Baseline, error) {
	absPath, err := os.Stat(req.LocalPath)
	if err != nil {
		return Baseline{}, fmt.Errorf("repository: local path %q: %w", req.LocalPath, err)
	}
	if !absPath.IsDir() {
		return Baseline{}, fmt.Errorf("repository: local path %q is not a directory", req.LocalPath)
	}

	// Pack on the host. `git bundle` is a transport, not a code path: it reads
	// objects and writes a file.
	bundlePath, err := os.CreateTemp("", "factory-repo-*.bundle")
	if err != nil {
		return Baseline{}, fmt.Errorf("repository: create temporary bundle: %w", err)
	}
	bundleName := bundlePath.Name()
	if err := bundlePath.Close(); err != nil {
		return Baseline{}, err
	}
	defer os.Remove(bundleName)

	if _, err := p.runHostCommand(ctx, req.LocalPath, "bundle", "create", bundleName, "--all"); err != nil {
		return Baseline{}, fmt.Errorf("repository: bundle local repository: %w", err)
	}
	bundleBytes, err := os.ReadFile(bundleName)
	if err != nil {
		return Baseline{}, fmt.Errorf("repository: read bundle: %w", err)
	}

	const sandboxBundle = "/workspace/.factory/repository.bundle"
	if err := sb.WriteFile(ctx, sandboxBundle, bundleBytes); err != nil {
		return Baseline{}, fmt.Errorf("repository: push bundle into sandbox: %w", err)
	}

	steps := []sandbox.Command{
		{Argv: []string{"mkdir", "-p", path.Dir(dest)}, Description: "create workspace"},
		{
			Argv:        []string{"git", "clone", "--no-tags", sandboxBundle, dest},
			Timeout:     5 * time.Minute,
			Description: "clone from bundle",
		},
		{
			// The bundle remote is a temporary artifact; drop it so the agent
			// cannot accidentally fetch or push through it.
			Argv:        []string{"git", "-C", dest, "remote", "remove", "origin"},
			Description: "remove bundle remote",
		},
	}
	if err := runAll(ctx, sb, steps); err != nil {
		return Baseline{}, err
	}

	return p.checkout(ctx, sb, Baseline{
		Kind:         SourceLocal,
		Source:       "local:" + req.LocalPath,
		RequestedRev: req.Revision,
	}, dest)
}

// checkout checks out the exact revision and records the baseline.
func (p *Provider) checkout(ctx context.Context, sb sandbox.Sandbox, base Baseline, dest string) (Baseline, error) {
	// Identity is set inside the sandbox so the agent can commit if it needs to.
	identity := []sandbox.Command{
		{Argv: []string{"git", "-C", dest, "config", "user.email", "factory@localhost"}, Description: "git identity email"},
		{Argv: []string{"git", "-C", dest, "config", "user.name", "Factory"}, Description: "git identity name"},
		{Argv: []string{"git", "-C", dest, "config", "advice.detachedHead", "false"}, Description: "quiet detached head"},
	}
	if err := runAll(ctx, sb, identity); err != nil {
		return Baseline{}, err
	}

	// Resolve the requested revision to a full SHA first so the recorded
	// baseline is unambiguous and the checkout is pinned.
	rev, err := runOne(ctx, sb, dest, append([]string{"rev-parse"}, revisionArgs(base.RequestedRev)...)...)
	if err != nil {
		return Baseline{}, err
	}
	sha := strings.TrimSpace(rev.Stdout)
	if sha == "" {
		return Baseline{}, fmt.Errorf("repository: could not resolve revision %q", base.RequestedRev)
	}

	checkout, err := sb.Execute(ctx, sandbox.Command{
		Argv:        []string{"git", "-C", dest, "checkout", "--detach", "--force", sha},
		Timeout:     2 * time.Minute,
		Description: "checkout exact revision",
	})
	if err != nil {
		return Baseline{}, fmt.Errorf("repository: checkout %s: %w", sha, err)
	}
	if !checkout.Succeeded() {
		return Baseline{}, fmt.Errorf("repository: checkout %s failed (exit %d): %s",
			sha, checkout.ExitCode, truncate(checkout.Stderr, 400))
	}

	status, err := runOne(ctx, sb, dest, "status", "--porcelain")
	if err != nil {
		return Baseline{}, err
	}
	branch, err := runOne(ctx, sb, dest, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return Baseline{}, err
	}

	base.SHA = sha
	base.Status = strings.TrimSpace(status.Stdout)
	base.Branch = strings.TrimSpace(branch.Stdout)
	base.PreparedAt = time.Now().UTC()
	base.LockfileHashes = p.hashLockfiles(ctx, sb, dest)
	return base, nil
}

// revisionArgs renders a revision for `git rev-parse`.
//
// A revision beginning with "-" would otherwise be parsed as an option, so
// --end-of-options terminates option parsing first. The result is always passed
// as a structured argv element, never as shell text.
func revisionArgs(revision string) []string {
	if strings.HasPrefix(revision, "-") {
		return []string{"--end-of-options", revision + "^{commit}"}
	}
	return []string{revision + "^{commit}"}
}

// lockfileCandidates are dependency lockfiles whose hashes are recorded so a
// run is reproducible even when the agent changes dependencies.
var lockfileCandidates = []string{
	"go.sum", "package-lock.json", "pnpm-lock.yaml", "yarn.lock",
	"requirements.txt", "poetry.lock", "Pipfile.lock", "Cargo.lock",
	"Gemfile.lock", "composer.lock",
}

func (p *Provider) hashLockfiles(ctx context.Context, sb sandbox.Sandbox, dest string) map[string]string {
	hashes := map[string]string{}
	for _, name := range lockfileCandidates {
		file := path.Join(dest, name)
		data, err := sb.ReadFile(ctx, file)
		if err != nil {
			continue
		}
		sum := sha256.Sum256(data)
		hashes[name] = hex.EncodeToString(sum[:])
	}
	if len(hashes) == 0 {
		return nil
	}
	return hashes
}

// HeadSHA returns the current HEAD of the repository in the sandbox.
func (p *Provider) HeadSHA(ctx context.Context, sb sandbox.Sandbox, dest string) (string, error) {
	out, err := runOne(ctx, sb, dest, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out.Stdout), nil
}

// Status returns `git status --porcelain` for the repository.
func (p *Provider) Status(ctx context.Context, sb sandbox.Sandbox, dest string) (string, error) {
	out, err := runOne(ctx, sb, dest, "status", "--porcelain")
	if err != nil {
		return "", err
	}
	return out.Stdout, nil
}

// Patch returns the complete diff of the agent's changes, including untracked
// files.
//
// `git add -A` stages everything and `git diff --cached <baseline>` renders
// changes against the revision the agent started from. Comparing with HEAD
// would let an agent hide its entire contribution by committing it first.
func (p *Provider) Patch(ctx context.Context, sb sandbox.Sandbox, dest, baseline string) (string, error) {
	if baseline == "" {
		return "", fmt.Errorf("repository: baseline revision is required to collect a patch")
	}
	steps := []sandbox.Command{
		{Argv: []string{"git", "-C", dest, "add", "-A"}, Timeout: 2 * time.Minute, Description: "stage all changes"},
		{
			Argv:        []string{"git", "-C", dest, "diff", "--cached", "--no-color", "--no-ext-diff", "--binary", baseline},
			Timeout:     2 * time.Minute,
			Description: "render patch",
		},
	}
	var patch string
	for _, step := range steps {
		exec, err := sb.Execute(ctx, step)
		if err != nil {
			return "", fmt.Errorf("repository: %s: %w", step.Description, err)
		}
		if !exec.Succeeded() {
			return "", fmt.Errorf("repository: %s failed (exit %d): %s",
				step.Description, exec.ExitCode, truncate(exec.Stderr, 400))
		}
		patch = exec.Stdout
	}
	return patch, nil
}

// runOne runs a single git subcommand against the repository.
func runOne(ctx context.Context, sb sandbox.Sandbox, dest string, args ...string) (sandbox.Execution, error) {
	argv := append([]string{"git", "-C", dest}, args...)
	exec, err := sb.Execute(ctx, sandbox.Command{Argv: argv})
	if err != nil {
		return sandbox.Execution{}, fmt.Errorf("repository: git %s: %w", strings.Join(args, " "), err)
	}
	if !exec.Succeeded() {
		return exec, fmt.Errorf("repository: git %s failed (exit %d): %s",
			strings.Join(args, " "), exec.ExitCode, truncate(exec.Stderr, 400))
	}
	return exec, nil
}

func runAll(ctx context.Context, sb sandbox.Sandbox, steps []sandbox.Command) error {
	for _, step := range steps {
		exec, err := sb.Execute(ctx, step)
		if err != nil {
			return fmt.Errorf("repository: %s: %w", step.Description, err)
		}
		if !exec.Succeeded() {
			return fmt.Errorf("repository: %s failed (exit %d): %s",
				step.Description, exec.ExitCode, truncate(exec.Stderr, 400))
		}
	}
	return nil
}

// HashTask returns a stable hash of the task text, recorded so a run's input is
// verifiable without storing the text in every field.
func HashTask(task string) string {
	sum := sha256.Sum256([]byte(task))
	return hex.EncodeToString(sum[:])
}

// runGitHost runs a git command on the factory host.
func runGitHost(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return stdout.String(), fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
