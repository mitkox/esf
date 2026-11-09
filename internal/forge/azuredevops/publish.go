package azuredevops

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mitkox/esf/internal/forge"
)

// PublicationBranchName derives the deterministic branch for one change
// attempt. The esf/ namespace is deliberate: branch policies can target it, and
// an operator can see that ESF owns the ref.
func PublicationBranchName(changeID string, attempt int) string {
	if attempt < 1 {
		attempt = 1
	}
	id := sanitizeRefSegment(changeID)
	if id == "" {
		id = "change"
	}
	if len(id) > 96 {
		id = id[:96]
	}
	return "esf/" + id + "-a" + strconv.Itoa(attempt)
}

// sanitizeRefSegment reduces an identity to characters git accepts in a ref
// path while keeping it recognizable. The result never contains a path
// separator, "..", or a trailing dot.
func sanitizeRefSegment(value string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.TrimSpace(value) {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_':
			b.WriteRune(r)
			lastDash = false
		case r == '.' || r == '-':
			if b.Len() > 0 && !lastDash {
				b.WriteRune(r)
				lastDash = r == '-'
			}
		default:
			if b.Len() > 0 && !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	return strings.Trim(b.String(), ".-")
}

// BuildPublicationOptions describes the host-side commit to build.
type BuildPublicationOptions struct {
	RepositoryURL string
	BaselineSHA   string
	Patch         []byte
	Branch        string
	ChangeID      string
	Attempt       int
	Message       string
	AuthorName    string
	AuthorEmail   string
	CommitTime    time.Time
	// CloneEnv is the environment for the baseline clone only. The connector
	// supplies an authenticated environment here so a private repository can be
	// cloned; it must not reach the non-network git steps, which keep the
	// sanitized environment. When empty the sanitized environment is used.
	CloneEnv []string
	// WorkDir is an optional caller-owned directory. When empty a temporary
	// directory is created and removed by Close.
	WorkDir string
}

// BuiltPublication is a commit built on the factory host from a durable patch.
type BuiltPublication struct {
	Branch             string
	Commit             string
	Tree               string
	BaselineTree       string
	ChangedPaths       []string
	GitmodulesBaseline []byte
	GitmodulesResult   []byte

	workDir string
	owned   bool
}

// Close removes a temporary checkout. It never touches a caller-owned WorkDir.
func (b *BuiltPublication) Close() error {
	if b == nil || !b.owned || b.workDir == "" {
		return nil
	}
	err := os.RemoveAll(b.workDir)
	b.owned = false
	return err
}

// BuildPublicationCommit applies a durable patch to a clean checkout at the
// recorded baseline and produces a deterministic commit.
//
// Hooks, filters, submodule recursion, ambient Git configuration and the
// developer's global identity are all disabled. The commit tree is compared
// with the tree written from the patched index before the commit is returned.
func BuildPublicationCommit(ctx context.Context, opts BuildPublicationOptions) (*BuiltPublication, error) {
	if err := validateBuildOptions(opts); err != nil {
		return nil, err
	}
	workDir := strings.TrimSpace(opts.WorkDir)
	owned := false
	if workDir == "" {
		var err error
		workDir, err = os.MkdirTemp("", "esf-publication-*")
		if err != nil {
			return nil, fmt.Errorf("azuredevops: create publication checkout: %w", err)
		}
		owned = true
	}
	built := &BuiltPublication{Branch: opts.Branch, workDir: workDir, owned: owned}
	cleanup := func(err error) (*BuiltPublication, error) {
		_ = built.Close()
		return nil, err
	}

	repoDir := filepath.Join(workDir, "repository")
	env := sanitizedGitEnv(workDir)
	cloneEnv := opts.CloneEnv
	if len(cloneEnv) == 0 {
		cloneEnv = env
	}
	if _, err := runGit(ctx, workDir, cloneEnv, nil, "clone", "--quiet", "--no-checkout", "--no-recurse-submodules", opts.RepositoryURL, repoDir); err != nil {
		return cleanup(fmt.Errorf("azuredevops: clone baseline: %w", err))
	}
	checkoutEnv := append(append([]string(nil), env...),
		"GIT_INDEX_FILE="+filepath.Join(repoDir, ".git", "esf-index"),
	)
	// Checkout with an isolated index so repository state cannot hide a
	// modification from the patch application.
	if _, err := runGit(ctx, repoDir, checkoutEnv, nil,
		"-c", "core.hooksPath="+os.DevNull,
		"-c", "core.fsmonitor=false",
		"-c", "submodule.recurse=false",
		"read-tree", "--reset", "-u", opts.BaselineSHA,
	); err != nil {
		return cleanup(fmt.Errorf("azuredevops: check out baseline: %w", err))
	}
	if _, err := runGit(ctx, repoDir, checkoutEnv, opts.Patch,
		"-c", "core.hooksPath="+os.DevNull,
		"-c", "submodule.recurse=false",
		"apply", "--index", "--binary", "--whitespace=nowarn", "-",
	); err != nil {
		return cleanup(fmt.Errorf("azuredevops: apply evidenced patch: %w", err))
	}
	treeOut, err := runGit(ctx, repoDir, checkoutEnv, nil, "write-tree")
	if err != nil {
		return cleanup(fmt.Errorf("azuredevops: write patched tree: %w", err))
	}
	tree := strings.TrimSpace(treeOut)
	if tree == "" {
		return cleanup(errors.New("azuredevops: patched tree is empty"))
	}

	message := strings.TrimSpace(opts.Message)
	if message == "" {
		message = fmt.Sprintf("ESF verified change %s (attempt %d)", opts.ChangeID, opts.Attempt)
	}
	commitEnv := append(append([]string(nil), env...),
		"GIT_AUTHOR_NAME="+opts.AuthorName,
		"GIT_AUTHOR_EMAIL="+opts.AuthorEmail,
		"GIT_COMMITTER_NAME="+opts.AuthorName,
		"GIT_COMMITTER_EMAIL="+opts.AuthorEmail,
		"GIT_AUTHOR_DATE="+gitDate(opts.CommitTime),
		"GIT_COMMITTER_DATE="+gitDate(opts.CommitTime),
	)
	commitOut, err := runGit(ctx, repoDir, commitEnv, []byte(message+"\n"),
		"-c", "core.hooksPath="+os.DevNull,
		"-c", "commit.gpgsign=false",
		"commit-tree", tree, "-p", opts.BaselineSHA, "-F", "-",
	)
	if err != nil {
		return cleanup(fmt.Errorf("azuredevops: create publication commit: %w", err))
	}
	commit := strings.TrimSpace(commitOut)
	if commit == "" {
		return cleanup(errors.New("azuredevops: commit-tree returned no commit"))
	}
	committedTree, err := runGit(ctx, repoDir, env, nil, "rev-parse", commit+"^{tree}")
	if err != nil {
		return cleanup(fmt.Errorf("azuredevops: verify commit tree: %w", err))
	}
	if strings.TrimSpace(committedTree) != tree {
		return cleanup(fmt.Errorf("azuredevops: commit tree %q does not match the patch-derived tree %q", strings.TrimSpace(committedTree), tree))
	}
	baselineTree, err := runGit(ctx, repoDir, env, nil, "rev-parse", opts.BaselineSHA+"^{tree}")
	if err != nil {
		return cleanup(fmt.Errorf("azuredevops: resolve baseline tree: %w", err))
	}
	changed, err := runGit(ctx, repoDir, env, nil, "diff", "--name-only", "--no-renames", "-z", opts.BaselineSHA, commit)
	if err != nil {
		return cleanup(fmt.Errorf("azuredevops: list published paths: %w", err))
	}
	built.Commit = commit
	built.Tree = tree
	built.BaselineTree = strings.TrimSpace(baselineTree)
	built.ChangedPaths = splitNUL(changed)
	built.GitmodulesBaseline = gitmodulesAt(ctx, repoDir, env, opts.BaselineSHA)
	built.GitmodulesResult = readOptionalFile(filepath.Join(repoDir, ".gitmodules"))
	return built, nil
}

// BuildBranch builds the deterministic host-side commit without touching the
// remote, so the factory can gate the built tree before any push.
//
// The host process clones a private repository itself, so the baseline clone
// uses the connector credential; the sandbox egress injection does not apply
// here. Every later git step keeps the sanitized environment.
func (c *Client) BuildBranch(ctx context.Context, req forge.BranchPublication) (forge.BranchBuild, error) {
	if c == nil {
		return forge.BranchBuild{}, errors.New("azuredevops: client is required")
	}
	buildRemote := strings.TrimSpace(req.RemoteURL)
	if buildRemote == "" {
		buildRemote = req.Repository
	}
	env, cleanup, err := c.gitAuthEnv()
	if err != nil {
		return forge.BranchBuild{}, err
	}
	defer cleanup()
	built, err := BuildPublicationCommit(ctx, BuildPublicationOptions{
		RepositoryURL: buildRemote,
		BaselineSHA:   req.BaselineSHA,
		Patch:         req.Patch,
		Branch:        req.Branch,
		ChangeID:      strings.TrimSpace(req.ChangeID),
		Attempt:       req.Attempt,
		Message:       req.Message,
		AuthorName:    req.AuthorName,
		AuthorEmail:   req.AuthorEmail,
		CommitTime:    req.CommitTime,
		CloneEnv:      env,
	})
	if err != nil {
		return forge.BranchBuild{}, err
	}
	return forge.BranchBuild{
		Branch:             req.Branch,
		Commit:             built.Commit,
		Tree:               built.Tree,
		ChangedPaths:       append([]string(nil), built.ChangedPaths...),
		GitmodulesBaseline: append([]byte(nil), built.GitmodulesBaseline...),
		GitmodulesResult:   append([]byte(nil), built.GitmodulesResult...),
		Handle:             built,
	}, nil
}

// PushBranch pushes a branch previously built by BuildBranch.
//
// A branch whose pull request is open is never force-pushed: an update would
// rewrite the revision a reviewer is looking at. When no pull request exists, a
// stale branch is replaced with a lease that names the observed remote value.
func (c *Client) PushBranch(ctx context.Context, req forge.BranchPublication, build forge.BranchBuild) (forge.BranchPublicationResult, error) {
	if c == nil {
		return forge.BranchPublicationResult{}, errors.New("azuredevops: client is required")
	}
	built, ok := build.Handle.(*BuiltPublication)
	if !ok || built == nil {
		return forge.BranchPublicationResult{}, errors.New("azuredevops: branch build handle is invalid")
	}
	defer built.Close()

	project, repo, err := c.repositoryCoordinates(req.Repository)
	if err != nil {
		return forge.BranchPublicationResult{}, err
	}
	remote := strings.TrimSpace(req.RemoteURL)
	if remote == "" {
		remote = req.Repository
	}
	env, cleanup, err := c.gitAuthEnv()
	if err != nil {
		return forge.BranchPublicationResult{}, err
	}
	defer cleanup()
	remoteSHA, err := c.remoteBranchSHA(ctx, env, remote, req.Branch)
	if err != nil {
		return forge.BranchPublicationResult{}, err
	}
	switch {
	case remoteSHA == built.Commit:
		// The deterministic retry landed on the same commit; nothing to push
		// and no pull request needs to be consulted.
	case remoteSHA != "":
		active, err := c.findActivePullRequest(ctx, project, repo, "refs/heads/"+req.Branch)
		if err != nil {
			return forge.BranchPublicationResult{}, err
		}
		if active != nil {
			return forge.BranchPublicationResult{}, fmt.Errorf("azuredevops: refusing to force-push %s while pull request %d is open", req.Branch, active.PullRequestID)
		}
		if _, err := runGit(ctx, built.workDir, env, nil, "-C", filepath.Join(built.workDir, "repository"), "push", "--porcelain",
			"--force-with-lease=refs/heads/"+req.Branch+":"+remoteSHA,
			remote, built.Commit+":refs/heads/"+req.Branch); err != nil {
			return forge.BranchPublicationResult{}, fmt.Errorf("azuredevops: replace stale publication branch: %w", err)
		}
	default:
		if _, err := runGit(ctx, built.workDir, env, nil, "-C", filepath.Join(built.workDir, "repository"), "push", "--porcelain",
			remote, built.Commit+":refs/heads/"+req.Branch); err != nil {
			return forge.BranchPublicationResult{}, fmt.Errorf("azuredevops: push publication branch: %w", err)
		}
	}
	verifiedSHA, err := c.remoteBranchSHA(ctx, env, remote, req.Branch)
	if err != nil {
		return forge.BranchPublicationResult{}, err
	}
	if verifiedSHA != built.Commit {
		return forge.BranchPublicationResult{}, fmt.Errorf("azuredevops: remote branch %s is %q, want %q", req.Branch, verifiedSHA, built.Commit)
	}
	return forge.BranchPublicationResult{
		Branch:             req.Branch,
		Commit:             built.Commit,
		Tree:               built.Tree,
		ChangedPaths:       append([]string(nil), built.ChangedPaths...),
		GitmodulesBaseline: append([]byte(nil), built.GitmodulesBaseline...),
		GitmodulesResult:   append([]byte(nil), built.GitmodulesResult...),
		AlreadyPresent:     remoteSHA == built.Commit,
	}, nil
}

// DiscardBranch releases a build that was not pushed.
func (c *Client) DiscardBranch(build forge.BranchBuild) {
	if built, ok := build.Handle.(*BuiltPublication); ok && built != nil {
		_ = built.Close()
	}
}

// PublishBranch builds and pushes the deterministic branch for a publication.
// It is the convenience path for callers that have no gate between the two
// steps; the factory itself always builds, gates, and then pushes.
func (c *Client) PublishBranch(ctx context.Context, req forge.BranchPublication) (forge.BranchPublicationResult, error) {
	build, err := c.BuildBranch(ctx, req)
	if err != nil {
		return forge.BranchPublicationResult{}, err
	}
	return c.PushBranch(ctx, req, build)
}

func validateBuildOptions(opts BuildPublicationOptions) error {
	if err := parseGitRemote(opts.RepositoryURL); err != nil {
		return err
	}
	if !validObjectID(opts.BaselineSHA) {
		return fmt.Errorf("azuredevops: baseline %q is not a git object id", opts.BaselineSHA)
	}
	if len(opts.Patch) == 0 {
		return errors.New("azuredevops: publication patch is empty")
	}
	if len(opts.Patch) > 64<<20 {
		return fmt.Errorf("azuredevops: publication patch exceeds %d bytes", 64<<20)
	}
	if !validRefName(opts.Branch) {
		return fmt.Errorf("azuredevops: branch %q is not a valid ref", opts.Branch)
	}
	if strings.TrimSpace(opts.AuthorName) == "" || strings.TrimSpace(opts.AuthorEmail) == "" {
		return errors.New("azuredevops: commit author identity is required")
	}
	if opts.CommitTime.IsZero() {
		return errors.New("azuredevops: commit timestamp is required")
	}
	return nil
}

func parseGitRemote(raw string) error {
	value := strings.TrimSpace(raw)
	if value == "" {
		return errors.New("azuredevops: repository is required")
	}
	if strings.HasPrefix(value, "-") || strings.ContainsAny(value, "\x00\r\n") {
		return errors.New("azuredevops: repository is not a safe git remote")
	}
	if strings.HasPrefix(value, "/") || strings.HasPrefix(value, "file://") {
		return nil
	}
	if strings.HasPrefix(value, "https://") || strings.HasPrefix(value, "http://") {
		return nil
	}
	return fmt.Errorf("azuredevops: repository %q is not an absolute path or http(s) URL", raw)
}

func validObjectID(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	for _, r := range value {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') && (r < 'A' || r > 'F') {
			return false
		}
	}
	return true
}

func validRefName(branch string) bool {
	branch = strings.TrimSpace(branch)
	if branch == "" || strings.HasPrefix(branch, "/") || strings.HasSuffix(branch, "/") ||
		strings.Contains(branch, "..") || strings.ContainsAny(branch, " ~^:?*[\\\x00\r\n") ||
		strings.HasSuffix(branch, ".") {
		return false
	}
	for _, part := range strings.Split(branch, "/") {
		if part == "" || part == "." || part == ".." || strings.HasPrefix(part, ".") || strings.HasSuffix(part, ".lock") {
			return false
		}
	}
	return true
}

// sanitizedGitEnv starts from an empty environment so no ambient GIT_*,
// credential helper, hook path, or developer identity survives into the
// publication checkout.
func sanitizedGitEnv(home string) []string {
	return []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + home,
		"LANG=C",
		"LC_ALL=C",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=" + os.DevNull,
		"GIT_CONFIG_SYSTEM=" + os.DevNull,
		"GIT_TERMINAL_PROMPT=0",
		"GIT_ASKPASS=/bin/false",
		"GIT_SSH_COMMAND=/bin/false",
		"GIT_ALLOW_PROTOCOL=https:http:file",
	}
}

func gitDate(t time.Time) string {
	return "@" + strconv.FormatInt(t.UTC().Unix(), 10) + " +0000"
}

func runGit(ctx context.Context, dir string, env []string, stdin []byte, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = env
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if len(detail) > 1024 {
			detail = detail[:1024]
		}
		if detail == "" {
			return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
		}
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, detail)
	}
	return stdout.String(), nil
}

func splitNUL(value string) []string {
	parts := strings.Split(value, "\x00")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, filepath.ToSlash(trimmed))
		}
	}
	return out
}

func gitmodulesAt(ctx context.Context, repoDir string, env []string, revision string) []byte {
	out, err := runGit(ctx, repoDir, env, nil, "show", revision+":.gitmodules")
	if err != nil {
		return nil
	}
	return []byte(out)
}

func readOptionalFile(path string) []byte {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return body
}

func (c *Client) remoteBranchSHA(ctx context.Context, env []string, remote, branch string) (string, error) {
	out, err := runGit(ctx, "", env, nil, "ls-remote", "--heads", remote, "refs/heads/"+branch)
	if err != nil {
		return "", fmt.Errorf("azuredevops: inspect remote branch: %w", err)
	}
	fields := strings.Fields(out)
	if len(fields) == 0 {
		return "", nil
	}
	return fields[0], nil
}

// gitAuthEnv creates an askpass helper that reads the credential from the
// child environment. The credential itself never appears in argv or a file.
func (c *Client) gitAuthEnv() ([]string, func(), error) {
	username, password := splitBasicCredential(c.credential.Value())
	if strings.TrimSpace(password) == "" {
		return nil, nil, errors.New("azuredevops: publishing credential is empty")
	}
	if strings.TrimSpace(username) == "" {
		username = "esf"
	}
	dir, err := os.MkdirTemp("", "esf-git-auth-*")
	if err != nil {
		return nil, nil, fmt.Errorf("azuredevops: create git auth helper: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	script := filepath.Join(dir, "askpass.sh")
	body := "#!/bin/sh\ncase \"$1\" in\n*[Uu]sername*) printf '%s\\n' \"$ESF_GIT_USERNAME\" ;;\n*) printf '%s\\n' \"$ESF_GIT_PASSWORD\" ;;\nesac\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("azuredevops: write git auth helper: %w", err)
	}
	env := sanitizedGitEnv(dir)
	filtered := env[:0]
	for _, entry := range env {
		if !strings.HasPrefix(entry, "GIT_ASKPASS=") {
			filtered = append(filtered, entry)
		}
	}
	env = append(filtered,
		"ESF_GIT_USERNAME="+username,
		"ESF_GIT_PASSWORD="+password,
		"GIT_ASKPASS="+script,
	)
	return env, cleanup, nil
}
