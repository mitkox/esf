package turnexec

import (
	"encoding/json"
	"errors"
	"github.com/mitkox/esf/internal/sandbox"
	"regexp"
	"strconv"
	"strings"
)

// TurnPolicy is the policy projection of an sf TurnSpec. It is operator-owned,
// received only from the configuration file, and generates the actual argv.
type TurnPolicy struct {
	Harness struct {
		Name           string `json:"name"`
		Model          string `json:"model,omitempty"`
		PermissionMode string `json:"permission_mode"`
		MaxTurns       int    `json:"max_turns,omitempty"`
	} `json:"harness"`
	Tools struct {
		Allow []string `json:"allow"`
		Deny  []string `json:"deny,omitempty"`
	} `json:"tools"`
	Sandbox struct {
		Network        string `json:"network"`
		TimeoutSeconds int    `json:"timeout_seconds"`
		Memory         string `json:"memory"`
		CPUs           string `json:"cpus"`
	} `json:"sandbox"`
	Output *struct {
		Schema map[string]any `json:"schema"`
	} `json:"output,omitempty"`
}

func policyDigest(p *TurnPolicy) string {
	b, _ := json.Marshal(p)
	var normalized map[string]any
	_ = json.Unmarshal(b, &normalized)
	return digestJSON(normalized)
}
func validatePolicy(p Profile) error {
	if p.TurnPolicy == nil {
		return nil
	}
	if p.InteractivePermissions || len(p.MCPServers) > 0 {
		return errors.New("compiled turn profiles cannot add connector or interactive capabilities")
	}
	for name := range p.EnvRefs {
		if name != "ANTHROPIC_API_KEY" && name != "ANTHROPIC_AUTH_TOKEN" {
			return errors.New("compiled turn profiles only accept model credential references")
		}
	}
	v := p.TurnPolicy
	if p.SelectionOnly || p.Harness != "claude-code" || v.Harness.Name != "claude-code" || len(p.Command) != 1 {
		return errors.New("turn policy requires exactly one Claude executable")
	}
	if v.Harness.PermissionMode != "default" && v.Harness.PermissionMode != "plan" && v.Harness.PermissionMode != "acceptEdits" {
		return errors.New("remote turn policy cannot bypass permissions")
	}
	if v.Harness.MaxTurns < 0 || v.Sandbox.TimeoutSeconds != p.TimeoutSeconds {
		return errors.New("unsupported turn resource policy")
	}
	if _, err := policyLimits(v); err != nil {
		return err
	}
	if v.Sandbox.Network != "none" && v.Sandbox.Network != "open" {
		return errors.New("unsupported turn network policy")
	}
	if (v.Sandbox.Network == "open") != p.Network.AllowsInternet() {
		return errors.New("profile network does not match turn policy")
	}
	return nil
}
func policyCommand(p Profile) []string {
	argv := append([]string{}, p.Command...)
	if p.TurnPolicy == nil {
		return argv
	}
	v := p.TurnPolicy
	argv = append(argv, "-p", "--verbose", "--output-format", "stream-json", "--permission-mode", v.Harness.PermissionMode, "--setting-sources", "")
	if v.Harness.Model != "" {
		argv = append(argv, "--model", v.Harness.Model)
	}
	if v.Harness.MaxTurns > 0 {
		argv = append(argv, "--max-turns", strconv.Itoa(v.Harness.MaxTurns))
	}
	for _, v := range []struct {
		flag  string
		tools []string
	}{{"--allowedTools", v.Tools.Allow}, {"--disallowedTools", v.Tools.Deny}} {
		if len(v.tools) > 0 {
			argv = append(argv, v.flag)
			argv = append(argv, v.tools...)
		}
	}
	if v.Output != nil {
		schema, _ := json.Marshal(v.Output.Schema)
		argv = append(argv, "--json-schema", string(schema))
	}
	return argv
}
func validMetadata(values map[string]string) bool {
	size := 0
	for key, value := range values {
		switch key {
		case "SF_STEP", "SF_PRODUCES", "SF_INPUTS", "SF_ANSWERS":
		default:
			return false
		}
		size += len(value)
	}
	return size <= 64<<10
}

// Resource sizes use the same explicit m/g and numeric CPU subset supported by
// the compiled sf contract. They are verified against actual VM allocation.
func policyLimits(p *TurnPolicy) (sandbox.Limits, error) {
	invalid := errors.New("unsupported compiled CPU/memory policy")
	if !regexp.MustCompile(`^[1-9][0-9]{0,6}[mg]$`).MatchString(p.Sandbox.Memory) || !regexp.MustCompile(`^[0-9]{1,3}(\.[0-9]{1,3})?$`).MatchString(p.Sandbox.CPUs) {
		return sandbox.Limits{}, invalid
	}
	size, _ := strconv.Atoi(p.Sandbox.Memory[:len(p.Sandbox.Memory)-1])
	if strings.HasSuffix(p.Sandbox.Memory, "g") {
		size *= 1024
	}
	parts := strings.Split(p.Sandbox.CPUs, ".")
	whole, _ := strconv.Atoi(parts[0])
	milli := whole * 1000
	if len(parts) == 2 {
		fraction, _ := strconv.Atoi(parts[1] + strings.Repeat("0", 3-len(parts[1])))
		milli += fraction
	}
	if size < 1 || size > 1048576 || milli < 1 || milli > 256000 {
		return sandbox.Limits{}, invalid
	}
	return sandbox.Limits{CPUMilli: milli, MemoryMiB: size}, nil
}
