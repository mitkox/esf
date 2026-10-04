package cube

import (
	"os/exec"
	"strings"
	"testing"
)

func TestBoundedCommandScriptPreservesSmallOutputAndExit(t *testing.T) {
	cmd := exec.Command("bash", "-lc", boundedCommandScript("printf hello; printf warning >&2; exit 7", 2048))
	stdout, err := cmd.Output()
	if code := cmd.ProcessState.ExitCode(); code != 7 || err == nil || string(stdout) != "hello" {
		t.Fatalf("stdout=%q exit=%d error=%v", stdout, code, err)
	}
}

func TestBoundedCommandScriptStopsExcessOutput(t *testing.T) {
	cmd := exec.Command("bash", "-lc", boundedCommandScript("head -c 100000 /dev/zero", 2048))
	output, err := cmd.CombinedOutput()
	if err == nil || cmd.ProcessState.ExitCode() != 122 {
		t.Fatalf("exit=%v output=%q", err, output)
	}
	if len(output) > 1500 || !strings.Contains(string(output), "ESF_OUTPUT_LIMIT_EXCEEDED") {
		t.Fatalf("output was not bounded or reported: %d bytes", len(output))
	}
}

func TestBoundedCommandScriptCapturesMultilineExit(t *testing.T) {
	// A script may end with exit. It must leave only its subshell, so the
	// wrapper can still bound and report the output.
	cmd := exec.Command("bash", "-lc", boundedCommandScript("head -c 100000 /dev/zero\nexit 0", 2048))
	output, err := cmd.CombinedOutput()
	if err == nil || cmd.ProcessState.ExitCode() != 122 {
		t.Fatalf("exit=%v output bytes=%d", err, len(output))
	}
	if len(output) > 1500 || !strings.Contains(string(output), "ESF_OUTPUT_LIMIT_EXCEEDED") {
		t.Fatalf("output was not bounded or reported: %d bytes", len(output))
	}
}
