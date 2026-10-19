// Command cube-test drives already-built Go test binaries inside a disposable
// Cube VM. Compilation can happen on the development host; test execution cannot.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/mitkox/esf/internal/factory"
	"github.com/mitkox/esf/internal/sandbox"
	"github.com/mitkox/esf/internal/sandbox/cube"
)

type binaries []string

func (b *binaries) String() string     { return fmt.Sprint([]string(*b)) }
func (b *binaries) Set(s string) error { *b = append(*b, s); return nil }
func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
func run() (err error) {
	var paths binaries
	var config, output, filter, source, pythonRuntime string
	var timeout time.Duration
	var checks bool
	var verbose bool
	flag.Var(&paths, "binary", "compiled test binary (repeatable)")
	flag.StringVar(&config, "config", "factory.toml", "local factory config")
	flag.StringVar(&output, "output", "cube-test.json", "evidence output")
	flag.StringVar(&filter, "run", ".", "Go test filter")
	flag.StringVar(&source, "source", "", "source tar.gz for tests that read repository fixtures")
	flag.StringVar(&pythonRuntime, "python-runtime", "", "optional standalone CPython tar.gz (bin/ and lib/) for release checks")
	flag.BoolVar(&checks, "checks", false, "run release inventory and qualification Python checks inside Cube")
	flag.BoolVar(&verbose, "verbose", false, "also print passing test logs; complete logs are always retained")
	flag.DurationVar(&timeout, "timeout", 10*time.Minute, "timeout per binary")
	flag.Parse()
	if len(paths) == 0 && !checks {
		return fmt.Errorf("provide at least one compiled test binary")
	}
	cfg, e := factory.LoadConfig(config)
	if e != nil {
		return e
	}
	cfg.Cube.CommandOutputBytes = 8 << 20
	p, e := cube.New(cfg.Cube)
	if e != nil {
		return e
	}
	defer p.Close()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	id := fmt.Sprintf("harness2-validation-%d", time.Now().UnixNano())
	closed := false
	report := map[string]any{"schema": "esf-cube-test/v1", "source_commit": os.Getenv("ESF_TEST_COMMIT"), "template": cfg.Cube.TemplateID, "started_at": time.Now().UTC()}
	defer func() {
		cc, stop := context.WithTimeout(context.WithoutCancel(ctx), 90*time.Second)
		defer stop()
		infos, e := p.List(cc)
		if e == nil {
			for _, i := range infos {
				if i.Metadata["test_id"] == id {
					if de := p.Destroy(cc, i.ID); de != nil {
						err = fmt.Errorf("cleanup: %w", de)
					}
				}
			}
			infos, e = p.List(cc)
		}
		remaining := 0
		for _, i := range infos {
			if i.Metadata["test_id"] == id {
				remaining++
			}
		}
		closed = e == nil && remaining == 0
		report["cleanup_verified"] = closed
		report["remaining_owned"] = remaining
		report["completed_at"] = time.Now().UTC()
		b, _ := json.MarshalIndent(report, "", "  ")
		if we := os.WriteFile(output, append(b, '\n'), 0600); we != nil {
			err = we
		}
		if !closed {
			err = fmt.Errorf("Cube cleanup unverified: %v", e)
		}
	}()
	f := false
	sb, e := p.Create(ctx, sandbox.Spec{Template: cfg.Cube.TemplateID, Metadata: map[string]string{"origin": "factory", "test_id": id}, IdleTimeout: timeout + time.Minute, Network: sandbox.Network{AllowInternet: &f}})
	if e != nil {
		return e
	}
	report["sandbox_id"] = sb.ID()
	if source != "" {
		b, e := os.ReadFile(source)
		if e != nil {
			return e
		}
		if e = sb.WriteFile(ctx, "/workspace/esf-source.tar.gz", b); e != nil {
			return e
		}
		x, e := sb.Execute(ctx, sandbox.Command{Argv: []string{"mkdir", "-p", "/workspace/source"}, Timeout: time.Minute})
		if e != nil || !x.Succeeded() {
			return fmt.Errorf("source directory: %v", e)
		}
		x, e = sb.Execute(ctx, sandbox.Command{Argv: []string{"tar", "-xzf", "/workspace/esf-source.tar.gz", "-C", "/workspace/source"}, Timeout: time.Minute})
		if e != nil || !x.Succeeded() {
			return fmt.Errorf("source extraction: %v", e)
		}
		sum := sha256.Sum256(b)
		report["source_sha256"] = hex.EncodeToString(sum[:])
	}
	var runs []map[string]any
	for index, path := range paths {
		pkg := ""
		if key, value, ok := strings.Cut(path, "="); ok {
			pkg, path = key, value
		}
		dir := "/workspace"
		if pkg != "" {
			if filepath.IsAbs(pkg) || strings.Contains(pkg, "..") {
				return fmt.Errorf("invalid package directory")
			}
			dir = filepath.Join("/workspace/source", pkg)
		}
		b, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		sum := sha256.Sum256(b)
		dest := fmt.Sprintf("/workspace/esf-tests/%d-%s", index, filepath.Base(path))
		if e = sb.WriteFile(ctx, dest, b); e != nil {
			return e
		}
		x, e := sb.Execute(ctx, sandbox.Command{Argv: []string{"chmod", "0700", dest}, Timeout: time.Minute})
		if e != nil || !x.Succeeded() {
			return fmt.Errorf("stage test binary: %v", e)
		}
		x, e = sb.Execute(ctx, sandbox.Command{Argv: []string{dest, "-test.v", "-test.run=" + filter, "-test.timeout=" + timeout.String()}, Dir: dir, Timeout: timeout + time.Minute, Env: map[string]string{"ESF_CUBE_TEST": "1"}})
		r := map[string]any{"binary": filepath.Base(path), "sha256": hex.EncodeToString(sum[:]), "exit_code": x.ExitCode, "stdout": x.Stdout, "stderr": x.Stderr, "duration": x.Duration.String()}
		runs = append(runs, r)
		report["runs"] = runs
		fmt.Printf("%s: exit=%d duration=%s passed=%d\n", filepath.Base(path), x.ExitCode, x.Duration, strings.Count(x.Stdout, "--- PASS:"))
		if verbose || !x.Succeeded() {
			fmt.Print(x.Stdout, x.Stderr)
		}
		if e != nil {
			return e
		}
		if !x.Succeeded() {
			return fmt.Errorf("Cube test binary %s failed", path)
		}
		if _, e = sb.Execute(ctx, sandbox.Command{Argv: []string{"rm", "--", dest}, Timeout: time.Minute}); e != nil {
			return e
		}
	}
	if checks {
		if source == "" {
			return fmt.Errorf("release checks require --source")
		}
		python := "python3"
		if pythonRuntime != "" {
			b, e := os.ReadFile(pythonRuntime)
			if e != nil {
				return e
			}
			if e = sb.WriteFile(ctx, "/workspace/python-runtime.tar.gz", b); e != nil {
				return e
			}
			x, e := sb.Execute(ctx, sandbox.Command{Script: "mkdir -p /workspace/python-runtime && tar -xzf /workspace/python-runtime.tar.gz -C /workspace/python-runtime && rm /workspace/python-runtime.tar.gz", Timeout: time.Minute})
			if e != nil || !x.Succeeded() {
				return fmt.Errorf("Python runtime extraction: %v %s", e, x.Stderr)
			}
			sum := sha256.Sum256(b)
			report["python_runtime_sha256"] = hex.EncodeToString(sum[:])
			python = "/workspace/python-runtime/bin/python3.12"
		}
		for _, argv := range [][]string{{python, "scripts/verify-release-inventory.py"}, {python, "-m", "unittest", "discover", "-s", "scripts/tests", "-p", "test_*.py"}} {
			x, e := sb.Execute(ctx, sandbox.Command{Argv: argv, Dir: "/workspace/source", Timeout: timeout})
			runs = append(runs, map[string]any{"check": argv, "exit_code": x.ExitCode, "stdout": x.Stdout, "stderr": x.Stderr, "duration": x.Duration.String()})
			report["runs"] = runs
			fmt.Printf("check %s: exit=%d\n%s%s", strings.Join(argv, " "), x.ExitCode, x.Stdout, x.Stderr)
			if e != nil {
				return e
			}
			if !x.Succeeded() {
				return fmt.Errorf("release check failed")
			}
		}
	}
	return nil
}
