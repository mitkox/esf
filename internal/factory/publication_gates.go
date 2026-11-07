package factory

import (
	"bufio"
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mitkox/esf/internal/evidence"
	artifacts "github.com/mitkox/esf/internal/factoryartifacts"
	"github.com/mitkox/esf/internal/secretfile"
	"github.com/mitkox/esf/internal/verification"
)

// PublicationViolationClass names why a patch may not be published.
type PublicationViolationClass string

const (
	// PublicationViolationCI is a change to a forge CI definition.
	PublicationViolationCI PublicationViolationClass = "ci"
	// PublicationViolationVerification is a change to a verification entry
	// point: a gate program, a script reached through a shell, a make or
	// package target, a task-runner configuration, or a referenced fixture.
	PublicationViolationVerification PublicationViolationClass = "verification"
	// PublicationViolationEvidence is a change to the evidence directory.
	PublicationViolationEvidence PublicationViolationClass = "evidence"
	// PublicationViolationSubmodule is a .gitmodules URL change that is not
	// operator-allowlisted.
	PublicationViolationSubmodule PublicationViolationClass = "submodule"
	// PublicationViolationPath is a changed path the gate cannot compare
	// safely.
	PublicationViolationPath PublicationViolationClass = "path"
)

// PublicationViolation is one reason a publication is refused.
type PublicationViolation struct {
	Class  PublicationViolationClass `json:"class"`
	Path   string                    `json:"path"`
	Detail string                    `json:"detail,omitempty"`
}

// PublicationGateResult records the protected-path verdict.
type PublicationGateResult struct {
	Passed     bool                        `json:"passed"`
	Violations []PublicationViolation      `json:"violations,omitempty"`
	Classes    []PublicationViolationClass `json:"classes,omitempty"`
}

// PublicationGateInput compares the actual published tree with the baseline.
//
// ChangedPaths comes from the host-built commit, not from patch text, so a
// rename, a committed change, or a patch that lies about its own paths is
// judged by what was actually built.
type PublicationGateInput struct {
	ChangedPaths          []string
	ProtectedPaths        []string
	BaselineGitmodules    []byte
	ResultGitmodules      []byte
	SubmoduleURLAllowlist []string
}

// EvaluatePublicationGates rejects protected-path changes.
func EvaluatePublicationGates(in PublicationGateInput) (PublicationGateResult, error) {
	var violations []PublicationViolation
	seen := map[string]bool{}
	add := func(v PublicationViolation) {
		key := string(v.Class) + "\x00" + v.Path + "\x00" + v.Detail
		if seen[key] {
			return
		}
		seen[key] = true
		violations = append(violations, v)
	}

	protected := normalizeProtectedPaths(in.ProtectedPaths)
	for _, rawPath := range in.ChangedPaths {
		changed, ok := normalizeChangedPath(rawPath)
		if !ok {
			add(PublicationViolation{Class: PublicationViolationPath, Path: rawPath, Detail: "path escapes the repository"})
			continue
		}
		if isCIDefinition(changed) {
			add(PublicationViolation{Class: PublicationViolationCI, Path: changed, Detail: "CI definitions are not agent-editable"})
			continue
		}
		if isEvidencePath(changed) {
			add(PublicationViolation{Class: PublicationViolationEvidence, Path: changed, Detail: "the evidence directory is factory-owned"})
			continue
		}
		if protectedPathMatches(changed, protected) {
			add(PublicationViolation{Class: PublicationViolationVerification, Path: changed, Detail: "verification entry point is not agent-editable"})
		}
	}

	if containsPath(in.ChangedPaths, ".gitmodules") {
		submoduleViolations, err := evaluateSubmoduleChanges(in.BaselineGitmodules, in.ResultGitmodules, in.SubmoduleURLAllowlist)
		if err != nil {
			return PublicationGateResult{}, err
		}
		for _, violation := range submoduleViolations {
			add(violation)
		}
	}

	sort.SliceStable(violations, func(i, j int) bool {
		if violations[i].Class != violations[j].Class {
			return violations[i].Class < violations[j].Class
		}
		if violations[i].Path != violations[j].Path {
			return violations[i].Path < violations[j].Path
		}
		return violations[i].Detail < violations[j].Detail
	})
	classes := make([]PublicationViolationClass, 0, len(violations))
	for _, violation := range violations {
		if len(classes) == 0 || classes[len(classes)-1] != violation.Class {
			classes = append(classes, violation.Class)
		}
	}
	return PublicationGateResult{Passed: len(violations) == 0, Violations: violations, Classes: classes}, nil
}

// PublicationProtectedPaths returns every repository entry point a verification
// step depends on, inferred from the configured argv vectors.
func (c Config) PublicationProtectedPaths() []string {
	seen := map[string]bool{}
	add := func(value string) {
		normalized := normalizeRepoPath(value)
		if normalized == "" || seen[normalized] {
			return
		}
		seen[normalized] = true
	}
	names := make([]string, 0, len(c.Verification))
	for name := range c.Verification {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		profile := c.Verification[name]
		for _, step := range profile.Steps {
			for _, candidate := range verificationEntryPoints(step) {
				add(candidate)
			}
		}
	}
	out := make([]string, 0, len(seen))
	for value := range seen {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func verificationEntryPoints(step verification.Step) []string {
	if len(step.Argv) == 0 {
		return nil
	}
	var out []string
	add := func(value string) {
		if candidate := relativeRepoPath(value); candidate != "" {
			out = append(out, applyStepDir(step.Dir, candidate))
		}
	}
	program := step.Argv[0]
	add(program)
	for _, arg := range step.Argv[1:] {
		if isShell(program) {
			continue
		}
		if looksLikeRepoPath(arg) {
			add(arg)
		}
	}
	out = append(out, runnerProtectedPaths(step.Dir, step.Argv)...)
	if isShell(program) {
		script, shellArgs := shellInvocation(step.Argv)
		if script != "" {
			add(script)
		}
		for _, word := range shellArgs {
			if looksLikeRepoPath(word) {
				add(word)
			}
		}
	}
	return out
}

func runnerProtectedPaths(dir string, argv []string) []string {
	program := strings.ToLower(filepath.Base(strings.TrimSpace(argv[0])))
	base := commandPrefix(argv)
	var candidates []string
	add := func(value string) {
		if candidate := relativeRepoPath(value); candidate != "" {
			candidates = append(candidates, candidate)
		}
	}
	switch program {
	case "make", "gmake":
		candidates = append(candidates, "Makefile", "makefile", "GNUmakefile")
		for i := 1; i < len(argv)-1; i++ {
			if argv[i] == "-f" || argv[i] == "--file" {
				add(argv[i+1])
			}
		}
	case "npm", "pnpm", "yarn", "bun", "npx":
		add(path.Join(base, "package.json"))
	case "task":
		candidates = append(candidates, "Taskfile.yml", "Taskfile.yaml")
	case "just":
		candidates = append(candidates, "justfile", "Justfile", ".justfile")
	case "mage":
		candidates = append(candidates, "magefile.go", "Magefile.go")
	case "tox":
		candidates = append(candidates, "tox.ini")
	case "nox":
		candidates = append(candidates, "noxfile.py")
	case "pytest":
		candidates = append(candidates, "pytest.ini", "pyproject.toml", "tox.ini", "setup.cfg", "conftest.py")
	case "cargo":
		candidates = append(candidates, "Cargo.toml")
	case "gradle", "gradlew":
		candidates = append(candidates, "build.gradle", "build.gradle.kts", "settings.gradle", "settings.gradle.kts", "gradle.properties")
	case "mvn", "mvnw":
		candidates = append(candidates, "pom.xml")
	case "composer":
		candidates = append(candidates, "composer.json")
	case "node":
		add(path.Join(base, "package.json"))
	case "ruby":
		add(path.Join(base, "Gemfile"))
	}
	out := make([]string, 0, len(candidates))
	for _, value := range candidates {
		if candidate := relativeRepoPath(value); candidate != "" {
			out = append(out, applyStepDir(dir, candidate))
		}
	}
	return out
}

func commandPrefix(argv []string) string {
	for i := 1; i < len(argv)-1; i++ {
		switch argv[i] {
		case "--prefix", "--cwd", "-C":
			prefix := strings.TrimSpace(argv[i+1])
			if candidate := relativeRepoPath(prefix); candidate != "" {
				return candidate
			}
		}
	}
	return ""
}

func isShell(program string) bool {
	switch strings.ToLower(filepath.Base(strings.TrimSpace(program))) {
	case "sh", "bash", "zsh", "dash", "ksh", "ash":
		return true
	default:
		return false
	}
}

// shellInvocation extracts the script path and path-like words from a shell
// argument vector. `bash script.sh` names the script; `bash -c "..."` carries
// the commands in the next argument.
func shellInvocation(argv []string) (string, []string) {
	script := ""
	var words []string
	for i := 1; i < len(argv); i++ {
		arg := strings.TrimSpace(argv[i])
		switch {
		case arg == "-c" && i+1 < len(argv):
			words = append(words, splitShellWords(argv[i+1])...)
			i++
		case strings.HasPrefix(arg, "-"):
			continue
		case script == "":
			script = arg
			words = append(words, splitShellWords(arg)...)
		default:
			words = append(words, splitShellWords(arg)...)
		}
	}
	return script, words
}

// splitShellWords is a deliberately small tokenizer. It removes quotes and
// splits on whitespace so a fixture path inside `sh -c "..."` is visible; it
// does not execute or expand anything.
func splitShellWords(script string) []string {
	var words []string
	var current strings.Builder
	var quote rune
	flush := func() {
		if current.Len() > 0 {
			words = append(words, current.String())
			current.Reset()
		}
	}
	escaped := false
	for _, r := range script {
		switch {
		case escaped:
			current.WriteRune(r)
			escaped = false
		case r == '\\' && quote != '\'':
			escaped = true
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				current.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote = r
		case r == ' ' || r == '\t' || r == '\n' || r == ';' || r == '|' || r == '&':
			flush()
		default:
			current.WriteRune(r)
		}
	}
	flush()
	return words
}

func looksLikeRepoPath(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || strings.HasPrefix(value, "-") || strings.HasPrefix(value, "/") ||
		strings.ContainsAny(value, "$*?[]`(){}<>|;&") || strings.Contains(value, "://") {
		return false
	}
	if value == "." || value == ".." || strings.Contains(value, "...") {
		return false
	}
	if strings.Contains(value, "/") {
		return true
	}
	switch strings.ToLower(path.Ext(value)) {
	case ".sh", ".bash", ".py", ".js", ".mjs", ".cjs", ".ts", ".go", ".rb", ".pl", ".php", ".yml", ".yaml", ".json", ".toml", ".mk", ".ini", ".cfg":
		return true
	default:
		return false
	}
}

func relativeRepoPath(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Trim(value, `"'`)
	if strings.HasPrefix(value, "./") {
		value = value[2:]
	}
	if value == "" || strings.HasPrefix(value, "/") || strings.Contains(value, "..") || strings.ContainsAny(value, "\x00\r\n") {
		return ""
	}
	cleaned := path.Clean(value)
	if cleaned == "." || cleaned == "" || strings.HasPrefix(cleaned, "../") {
		return ""
	}
	return cleaned
}

func applyStepDir(dir, value string) string {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return value
	}
	if relative := relativeRepoPath(dir); relative != "" {
		return path.Join(relative, value)
	}
	return value
}

func normalizeProtectedPaths(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		normalized := normalizeRepoPath(value)
		if normalized == "" || seen[normalized] {
			continue
		}
		seen[normalized] = true
		out = append(out, normalized)
	}
	sort.Strings(out)
	return out
}

func normalizeRepoPath(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "./")
	if value == "" || strings.HasPrefix(value, "/") || strings.Contains(value, "..") || strings.ContainsAny(value, "\x00\r\n") {
		return ""
	}
	cleaned := path.Clean(value)
	if cleaned == "." || cleaned == "" || strings.HasPrefix(cleaned, "../") {
		return ""
	}
	return cleaned
}

func normalizeChangedPath(value string) (string, bool) {
	changed := strings.TrimSpace(filepath.ToSlash(value))
	changed = strings.TrimPrefix(changed, "./")
	if changed == "" || strings.HasPrefix(changed, "/") || strings.Contains(changed, "..") || strings.ContainsAny(changed, "\x00\r\n") {
		return "", false
	}
	return path.Clean(changed), true
}

func protectedPathMatches(changed string, protected []string) bool {
	for _, candidate := range protected {
		if changed == candidate || strings.HasPrefix(changed, candidate+"/") {
			return true
		}
	}
	return false
}

func isEvidencePath(changed string) bool {
	return changed == "evidence" || strings.HasPrefix(changed, "evidence/")
}

func isCIDefinition(changed string) bool {
	lower := strings.ToLower(changed)
	for _, prefix := range []string{
		".github/workflows/", ".github/actions/", ".gitlab/ci/", ".azure-pipelines/",
		".azuredevops/", ".circleci/", ".buildkite/", ".ci/",
	} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	switch lower {
	case ".gitlab-ci.yml", ".gitlab-ci.yaml", "azure-pipelines.yml", "azure-pipelines.yaml",
		".azure-pipelines.yml", ".azure-pipelines.yaml", "jenkinsfile",
		"bitbucket-pipelines.yml", ".travis.yml", "appveyor.yml", ".drone.yml",
		".woodpecker.yml", "cloudbuild.yaml", "cloudbuild.yml", ".cirrus.yml":
		return true
	default:
		return false
	}
}

func containsPath(paths []string, wanted string) bool {
	for _, value := range paths {
		if normalizeRepoPath(value) == wanted {
			return true
		}
	}
	return false
}

func evaluateSubmoduleChanges(baseline, result []byte, allowlist []string) ([]PublicationViolation, error) {
	before, err := parseGitmodules(baseline)
	if err != nil {
		return nil, fmt.Errorf("publication gate: baseline .gitmodules: %w", err)
	}
	after, err := parseGitmodules(result)
	if err != nil {
		return nil, fmt.Errorf("publication gate: resulting .gitmodules: %w", err)
	}
	names := map[string]bool{}
	for name := range before {
		names[name] = true
	}
	for name := range after {
		names[name] = true
	}
	sorted := make([]string, 0, len(names))
	for name := range names {
		sorted = append(sorted, name)
	}
	sort.Strings(sorted)
	var violations []PublicationViolation
	for _, name := range sorted {
		oldURL, newURL := before[name], after[name]
		if oldURL == newURL {
			continue
		}
		if submoduleURLAllowed(oldURL, allowlist) && submoduleURLAllowed(newURL, allowlist) {
			continue
		}
		violations = append(violations, PublicationViolation{
			Class:  PublicationViolationSubmodule,
			Path:   ".gitmodules",
			Detail: fmt.Sprintf("submodule %q URL changed from %q to %q without an allowlist entry", name, oldURL, newURL),
		})
	}
	return violations, nil
}

func parseGitmodules(body []byte) (map[string]string, error) {
	urls := map[string]string{}
	if len(bytes.TrimSpace(body)) == 0 {
		return urls, nil
	}
	scanner := bufio.NewScanner(bytes.NewReader(body))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	current := ""
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[submodule ") {
			closing := strings.LastIndex(line, "]")
			if closing < 0 {
				return nil, fmt.Errorf("malformed submodule section %q", line)
			}
			current = strings.Trim(strings.TrimSpace(line[len("[submodule "):closing]), `"`)
			if current == "" {
				return nil, fmt.Errorf("submodule section has no name")
			}
			continue
		}
		if strings.HasPrefix(line, "[") {
			current = ""
			continue
		}
		if current == "" {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || !strings.EqualFold(strings.TrimSpace(key), "url") {
			continue
		}
		url := strings.TrimSpace(value)
		if url == "" || strings.ContainsAny(url, "\x00\r\n") {
			return nil, fmt.Errorf("submodule %q has an invalid URL", current)
		}
		urls[current] = url
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return urls, nil
}

func submoduleURLAllowed(value string, allowlist []string) bool {
	if strings.TrimSpace(value) == "" {
		// Adding or removing a submodule has only one URL; the other side is
		// allowed by definition.
		return true
	}
	for _, raw := range allowlist {
		allowed := strings.TrimSpace(raw)
		switch {
		case allowed == value:
			return true
		case strings.HasSuffix(allowed, "*") && strings.HasPrefix(value, strings.TrimSuffix(allowed, "*")):
			return true
		}
	}
	return false
}

// verifyEvidenceAgainstManifest checks the just-written signed bundle against
// the durable run record. It returns the bundle digest that goes in the pull
// request body.
func (a *Activities) verifyEvidenceAgainstManifest(store artifacts.Store, manifest RunManifest) (string, int, error) {
	keyBody, err := readEvidenceSigningKey(a.cfg)
	if err != nil {
		return "", 0, err
	}
	privateKey, err := evidence.ParsePrivateKey(keyBody)
	if err != nil {
		return "", 0, err
	}
	publicKey, ok := privateKey.Public().(ed25519.PublicKey)
	if !ok {
		return "", 0, fmt.Errorf("publication gate: signing key has no ed25519 public key")
	}
	bundleBody, err := store.Read(ArtifactEvidenceBundle)
	if err != nil {
		return "", 0, fmt.Errorf("publication gate: read evidence bundle: %w", err)
	}
	var envelope evidence.Envelope
	if err := json.Unmarshal(bundleBody, &envelope); err != nil {
		return "", 0, fmt.Errorf("publication gate: decode evidence bundle: %w", err)
	}
	statement, err := evidence.Verify(&envelope, publicKey)
	if err != nil {
		return "", 0, fmt.Errorf("publication gate: evidence bundle does not verify: %w", err)
	}
	if err := compareEvidenceToManifest(statement, manifest); err != nil {
		return "", 0, err
	}
	patchPath := manifest.Patch
	if patchPath == "" {
		patchPath = ArtifactPatch
	}
	patch, err := store.Read(patchPath)
	if err != nil {
		return "", 0, fmt.Errorf("publication gate: read evidenced patch: %w", err)
	}
	patchDigest := sha256.Sum256(patch)
	if len(statement.Subject) == 0 || statement.Subject[0].Name != "changes.patch" ||
		statement.Subject[0].Digest["sha256"] != hex.EncodeToString(patchDigest[:]) {
		return "", 0, fmt.Errorf("publication gate: evidence patch subject does not match the durable patch")
	}
	bundleDigest := sha256.Sum256(bundleBody)
	return "sha256:" + hex.EncodeToString(bundleDigest[:]), len(statement.Predicate.Verification.Gates), nil
}

func compareEvidenceToManifest(statement *evidence.Statement, manifest RunManifest) error {
	predicate := statement.Predicate
	changeID := manifest.ChangeID
	if changeID == "" {
		changeID = manifest.RunID
	}
	for _, check := range []struct {
		name string
		got  string
		want string
	}{
		{"run id", predicate.Run.RunID, manifest.RunID},
		{"change id", predicate.Run.ChangeID, changeID},
		{"run state", predicate.Run.State, string(manifest.FactoryResult)},
		{"base commit", predicate.Input.BaseCommit, manifest.BaselineSHA},
		{"result commit", predicate.Input.ResultCommit, manifest.ResultingSHA},
		{"task digest", predicate.Input.TaskSHA256, manifest.TaskHash},
		{"verification profile", predicate.Verification.Profile, manifest.VerificationProfile},
	} {
		if check.got != check.want {
			return fmt.Errorf("publication gate: evidence %s %q does not match manifest %q", check.name, check.got, check.want)
		}
	}
	if predicate.Verification.Passed != (manifest.VerificationResult == OutcomeSuccess) {
		return fmt.Errorf("publication gate: evidence verification result does not match the manifest")
	}
	if !predicate.Run.StartedAt.Equal(manifest.StartedAt) || !predicate.Run.CompletedAt.Equal(manifest.CompletedAt) {
		return fmt.Errorf("publication gate: evidence run timestamps do not match the manifest")
	}
	return nil
}

func readEvidenceSigningKey(cfg Config) ([]byte, error) {
	if !cfg.Evidence.configured() {
		return nil, fmt.Errorf("publication gate: evidence signing is not configured")
	}
	return secretfile.Read(cfg.Evidence.SigningKeyFile, maxEvidenceSigningKeyBytes)
}

// AssertSandboxSecretIsolation fails closed when a signing-key path or a
// publishing credential is present in an environment destined for a Cube
// sandbox. The check inspects both variable names and values; a path can be
// just as useful to an attacker as the key itself.
func (c Config) AssertSandboxSecretIsolation(env map[string]string) error {
	secretPaths := make([]string, 0, 1+len(c.Connectors))
	if value := strings.TrimSpace(c.Evidence.SigningKeyFile); value != "" {
		secretPaths = append(secretPaths, value)
	}
	for _, name := range sortedForgeNames(c.Connectors) {
		if value := strings.TrimSpace(c.Connectors[name].TokenFile); value != "" {
			secretPaths = append(secretPaths, value)
		}
	}
	forbiddenNames := map[string]bool{
		"azure_devops_ext_pat": true,
		"azure_devops_pat":     true,
		"ado_pat":              true,
		"system_accesstoken":   true,
		"git_password":         true,
		"git_token":            true,
		"factory_signing_key":  true,
		"esf_signing_key":      true,
		"esf_publishing_token": true,
	}
	var problems []string
	for key, value := range env {
		lowerKey := strings.ToLower(strings.TrimSpace(key))
		if forbiddenNames[lowerKey] ||
			(strings.Contains(lowerKey, "signing") && strings.Contains(lowerKey, "key")) ||
			(strings.Contains(lowerKey, "azure") && strings.Contains(lowerKey, "pat")) {
			problems = append(problems, fmt.Sprintf("environment variable %q looks like a signing or publishing credential", key))
		}
		for _, secretPath := range secretPaths {
			if secretPath != "" && (strings.Contains(value, secretPath) || strings.Contains(value, "file://"+secretPath)) {
				problems = append(problems, fmt.Sprintf("environment variable %q exposes a host secret path", key))
			}
		}
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return fmt.Errorf("sandbox environment violates secret isolation: %s", strings.Join(problems, "; "))
	}
	return nil
}

// validateSecretIsolationConfig rejects configuration that would carry a host
// secret path into sandbox-owned setup text. Runtime environment checks are
// stronger; this catches the configuration-level mistake at startup.
func (c Config) validateSecretIsolationConfig() error {
	paths := make([]string, 0, 1+len(c.Connectors))
	if value := strings.TrimSpace(c.Evidence.SigningKeyFile); value != "" {
		paths = append(paths, value)
	}
	for _, name := range sortedForgeNames(c.Connectors) {
		if value := strings.TrimSpace(c.Connectors[name].TokenFile); value != "" {
			paths = append(paths, value)
		}
	}
	setup := c.Sandbox.SetupScript
	for _, secretPath := range paths {
		if strings.Contains(setup, secretPath) {
			return fmt.Errorf("sandbox.setup_script references host secret path %q", secretPath)
		}
	}
	return nil
}
