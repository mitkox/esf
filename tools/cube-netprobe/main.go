// Command cube-netprobe is a discovery spike for the CubeSandbox egress policy
// model on THIS deployment. It answers one narrow question the factory design
// depends on: can a sandbox reach a factory-hosted service, and if so, only
// when the operator explicitly allow-lists it?
//
// It is read-only with respect to Cube configuration: it creates its own
// throwaway sandboxes, destroys them, and never mutates templates or the
// deployment. It exits non-zero if cleanup fails (a leaked VM is a failure).
//
// Usage:
//
//	go run ./tools/cube-netprobe -bind 0.0.0.0:18080
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/mitkox/esf/internal/sandbox/cube"
	cubesandbox "github.com/tencentcloud/CubeSandbox/sdk/go"
)

func main() {
	bind := flag.String("bind", "0.0.0.0:18080", "host address the probe service listens on")
	apiURL := flag.String("api-url", envOr("CUBE_API_URL", "http://127.0.0.1:4000"), "Cube API URL")
	templateID := flag.String("template", os.Getenv("CUBE_TEMPLATE_ID"), "Cube template ID")
	hostIP := flag.String("host-ip", envOr("CUBE_PROBE_HOST_IP", "192.0.2.10"), "host address to reach from the sandbox")
	version := flag.String("cube-version", os.Getenv("FACTORY_CUBE_VERSION"), "operator-recorded Cube release (enables release compatibility)")
	flag.Parse()

	if err := run(*bind, *apiURL, *templateID, *hostIP, *version); err != nil {
		fmt.Fprintf(os.Stderr, "\nNETPROBE FAILED: %v\n", err)
		os.Exit(1)
	}
}

func run(bind, apiURL, templateID, hostIP, version string) error {
	if templateID == "" {
		return errors.New("CUBE_TEMPLATE_ID is required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	// Resolve on the driver so a blocked sandbox DNS resolver cannot be
	// mistaken for a successful internet-denial policy. Curl still verifies
	// example.com's certificate and uses its hostname as TLS SNI.
	dnsCtx, dnsCancel := context.WithTimeout(ctx, 15*time.Second)
	publicIPs, err := net.DefaultResolver.LookupIP(dnsCtx, "ip4", "example.com")
	dnsCancel()
	if err != nil || len(publicIPs) == 0 {
		return fmt.Errorf("resolve public probe target: addresses=%d error=%v", len(publicIPs), err)
	}
	publicIP := publicIPs[0].String()

	// 1. Start a marker service on the host.
	port := strings.TrimPrefix(bind, "0.0.0.0:")
	listener, err := net.Listen("tcp", bind)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", bind, err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "factory-host-service-ok")
	})}
	go func() { _ = server.Serve(listener) }()
	defer func() {
		shutdownCtx, c := context.WithTimeout(context.Background(), 3*time.Second)
		defer c()
		_ = server.Shutdown(shutdownCtx)
	}()
	fmt.Printf("Host marker service listening on %s (sandbox target http://%s:%s)\n\n", bind, hostIP, port)

	cfg := cubesandbox.NewConfigFromEnv()
	cfg.APIURL = apiURL
	cfg.TemplateID = templateID
	client := cubesandbox.NewClient(cfg)
	defer client.Close()

	if info, err := client.GetTemplate(ctx, templateID); err == nil {
		fmt.Printf("Template %s: status=%s instanceType=%s networkType=%q allowInternetAccess=%v\n\n",
			info.TemplateID, info.Status, info.InstanceType, info.NetworkType, derefBool(info.AllowInternetAccess))
	}

	type scenario struct {
		name         string
		opts         cubesandbox.CreateOptions
		wantHost     bool
		wantInternet *bool
	}

	scenarios := []scenario{
		{
			name:     "default policy (no allowlist)",
			opts:     cubesandbox.CreateOptions{TemplateID: templateID, Timeout: cubesandbox.DurationPtr(5 * time.Minute)},
			wantHost: false,
		},
		{
			name: "explicit allow_out=[host]",
			opts: cubesandbox.CreateOptions{
				TemplateID: templateID,
				Timeout:    cubesandbox.DurationPtr(5 * time.Minute),
				Network:    cubesandbox.NetworkOptions{AllowOut: []string{hostIP}},
			},
			wantHost: true,
		},
		{
			name: "deny internet + allow_out=[host]",
			opts: cubesandbox.CreateOptions{
				TemplateID:          templateID,
				Timeout:             cubesandbox.DurationPtr(5 * time.Minute),
				AllowInternetAccess: boolPtr(false),
				Network:             cubesandbox.NetworkOptions{AllowOut: []string{hostIP}},
			},
			wantHost:     true,
			wantInternet: boolPtr(false),
		},
	}

	failures := 0
	for _, sc := range scenarios {
		sc.opts = cube.CompatibleCreateOptions(sc.opts, version)
		fmt.Printf("=== scenario: %s ===\n", sc.name)
		reachable, internet, sandboxID, err := probeScenario(ctx, client, sc.opts, hostIP, port, publicIP)
		if err != nil {
			fmt.Printf("  error: %v\n\n", err)
			failures++
			continue
		}
		fmt.Printf("  sandbox=%s\n", sandboxID)
		fmt.Printf("  host service reachable = %v (expected %v)\n", reachable, sc.wantHost)
		fmt.Printf("  public internet reachable = %v\n", internet)
		if reachable != sc.wantHost {
			fmt.Printf("  UNEXPECTED\n")
			failures++
		}
		if sc.wantInternet != nil && internet != *sc.wantInternet {
			fmt.Printf("  UNEXPECTED public internet policy (expected %v)\n", *sc.wantInternet)
			failures++
		}
		fmt.Println()
	}

	if failures > 0 {
		return fmt.Errorf("%d scenario(s) behaved unexpectedly", failures)
	}
	fmt.Println("NETPROBE OK")
	return nil
}

// probeScenario creates one sandbox, tests reachability, and always destroys it.
func probeScenario(ctx context.Context, client *cubesandbox.Client, opts cubesandbox.CreateOptions, hostIP, port, publicIP string) (reachable, internet bool, sandboxID string, err error) {
	before, err := client.List(ctx)
	if err != nil {
		return false, false, "", fmt.Errorf("list before create: %w", err)
	}

	sandbox, err := client.Create(ctx, opts)
	if err != nil {
		return false, false, "", fmt.Errorf("create: %w", err)
	}
	sandboxID = sandbox.SandboxID

	defer func() {
		killCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		if killErr := sandbox.Kill(killCtx); killErr != nil {
			err = errors.Join(err, fmt.Errorf("destroy %s: %w", sandboxID, killErr))
			return
		}
		after, listErr := client.List(killCtx)
		if listErr != nil {
			err = errors.Join(err, fmt.Errorf("list after destroy: %w", listErr))
			return
		}
		if len(after) != len(before) {
			err = errors.Join(err, fmt.Errorf("sandbox count changed %d -> %d", len(before), len(after)))
		}
	}()

	// The host marker service answers a plain HTTP GET.
	hostCheck := fmt.Sprintf(
		`code=$(curl --noproxy '*' -s -m 8 -o /dev/null -w '%%{http_code}' http://%s:%s/ 2>/dev/null); echo "host=$code"`,
		hostIP, port)
	out, err := sandbox.Commands().Run(ctx, hostCheck, cubesandbox.CommandOptions{})
	if err != nil {
		return false, false, sandboxID, fmt.Errorf("host probe: %w", err)
	}
	reachable = strings.Contains(out.Stdout, "host=200")

	publicCheck := fmt.Sprintf(`code=$(curl --noproxy '*' --resolve example.com:443:%s -s -m 10 -o /dev/null -w '%%{http_code}' https://example.com 2>/dev/null); echo "net=$code"`, publicIP)
	netOut, err := sandbox.Commands().Run(ctx, publicCheck,
		cubesandbox.CommandOptions{})
	if err != nil {
		return reachable, false, sandboxID, fmt.Errorf("internet probe: %w", err)
	}
	internet = strings.Contains(netOut.Stdout, "net=200")

	return reachable, internet, sandboxID, nil
}

func envOr(name, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	return fallback
}

func boolPtr(v bool) *bool { return &v }
func derefBool(v *bool) any {
	if v == nil {
		return "unset"
	}
	return *v
}
