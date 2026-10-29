// Command cube-security-qualify tests the preview-route prerequisite for issue
// #50. It drives owned Cube VMs; it never runs target or assessment code locally.
// A route-only result cannot enable the dependent security assessment stage.
package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"embed"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mitkox/esf/internal/sandbox/cube"
	"github.com/mitkox/esf/internal/securityassessment/route"
	"github.com/pelletier/go-toml/v2"
	cubesandbox "github.com/tencentcloud/CubeSandbox/sdk/go"
)

//go:embed assets/probe.py
var assets embed.FS

//go:embed assets/isolation_probe.py
var isolationAssets embed.FS

type check struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

type report struct {
	Schema             string            `json:"schema"`
	Owner              string            `json:"owner"`
	Started            time.Time         `json:"started"`
	Template           string            `json:"template"`
	CubeVersion        string            `json:"cube_version"`
	Hashes             map[string]string `json:"sha256"`
	Checks             []check           `json:"checks"`
	Owned              []string          `json:"allocated_ids"`
	Remaining          []string          `json:"remaining_owned_ids"`
	Unrelated          []string          `json:"unrelated_ids"`
	Decision           string            `json:"decision"`
	IntegrationEnabled bool              `json:"integration_enabled"`
	PendingAllocation  bool              `json:"pending_allocation"`
	RouteGuard         *route.HostGuard  `json:"route_guard,omitempty"`
	AssessorTemplate   string            `json:"assessor_template,omitempty"`
	RouteQualified     bool              `json:"route_qualified"`
}

func (r *report) decide() {
	r.IntegrationEnabled = false
	r.Decision = "incomplete"
	for _, c := range r.Checks {
		if c.Status == "fail" {
			r.Decision = "no-go"
		}
	}
}

func nonce() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func digest(data []byte) string { return fmt.Sprintf("%x", sha256.Sum256(data)) }

func newReport(path string) (*report, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	if err := handoffOwnership(f); err != nil {
		f.Close()
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	r := &report{Schema: "esf.cube-security-route/v1", Owner: "qualification-" + nonce(), Started: time.Now().UTC(), Hashes: map[string]string{}}
	return r, r.save(path)
}

func (r *report) save(path string) error {
	r.decide()
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".qualification-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err := handoffOwnership(f); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(append(data, '\n')); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

func ownedIDs(infos []cubesandbox.SandboxInfo, owner string) []string {
	var ids []string
	if owner == "" {
		return ids
	}
	for _, info := range infos {
		if info.Metadata["esf.security.owner"] == owner {
			ids = append(ids, info.SandboxID)
		}
	}
	sort.Strings(ids)
	return ids
}

func main() {
	config := flag.String("config", "factory.toml", "local factory configuration; never copied into a VM")
	output := flag.String("report", "", "new private receipt path; existing files are never overwritten")
	fixture := flag.String("fixture", "testdata/security-assessment/tenant-api/server.py", "synthetic target source")
	cleanup := flag.Bool("cleanup-only", false, "reconcile owned VMs from an interrupted receipt, without creating any")
	caFile := flag.String("ca-file", "", "optional operator public CA certificates for preview TLS; private keys are rejected")
	assessmentTemplate := flag.String("assessment-template", "", "dedicated assessment runtime template (default: configured template for route-only checks)")
	guardSocket := flag.String("guard-socket", "", "operator-started scoped root guard service socket")
	flag.Parse()
	if *output == "" {
		fmt.Fprintln(os.Stderr, "--report is required")
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, *config, *output, *fixture, *caFile, *assessmentTemplate, *guardSocket, *cleanup); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, configPath, reportPath, fixturePath, caFile, assessmentTemplate, guardSocket string, cleanupOnly bool) (returnErr error) {
	if err := route.CheckHostGuardSupport(); err != nil {
		return err
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}
	var config struct {
		Cube cube.Config `toml:"cube"`
	}
	if err := toml.Unmarshal(data, &config); err != nil {
		return err
	}
	cfg := config.Cube
	if err := cfg.Validate(); err != nil {
		return err
	}
	if cfg.ProxyScheme != "https" || cfg.ProxyNodeIP == "" {
		return errors.New("route qualification requires an explicit HTTPS proxy")
	}
	client := cubesandbox.NewClient(cubesandbox.Config{APIURL: cfg.APIURL, APIKey: cfg.APIKey, TemplateID: cfg.TemplateID,
		ProxyNodeIP: cfg.ProxyNodeIP, ProxyPortHTTP: cfg.ProxyPortHTTP, ProxyScheme: cfg.ProxyScheme, SandboxDomain: cfg.SandboxDomain, RequestTimeout: 20 * time.Second})
	defer client.Close()
	var r *report
	if cleanupOnly {
		data, err := os.ReadFile(reportPath)
		if err != nil {
			return err
		}
		r = &report{}
		if err := json.Unmarshal(data, r); err != nil {
			return err
		}
		if r.Schema != "esf.cube-security-route/v1" || !validOwner(r.Owner) {
			return errors.New("invalid reconciliation receipt")
		}
		if r.RouteGuard != nil && (r.RouteGuard.Owner != r.Owner || r.RouteGuard.Port != 9445) {
			return errors.New("route guard and receipt ownership mismatch")
		}
	} else {
		r, err = newReport(reportPath)
		if err != nil {
			return err
		}
		r.Template, r.CubeVersion = cfg.TemplateID, cfg.Version
	}
	// Register reconciliation before allocation, including an unknown create
	// acknowledgement. The unpredictable owner was durably saved before Create.
	var broker *route.Broker
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		if broker != nil {
			returnErr = errors.Join(returnErr, broker.Close(cleanupCtx))
		}
		returnErr = errors.Join(returnErr, reconcile(cleanupCtx, client, cfg, r))
		if r.RouteGuard != nil {
			if err := r.RouteGuard.Close(cleanupCtx); err != nil {
				r.Checks = append(r.Checks, check{Name: "route-cleanup", Status: "incomplete"})
				returnErr = errors.Join(returnErr, err)
			} else {
				r.Checks = append(r.Checks, check{Name: "route-cleanup", Status: "pass", Detail: "owned address and firewall chain removed"})
			}
		}
		if returnErr != nil || r.PendingAllocation || len(r.Remaining) > 0 {
			r.RouteQualified = false
		}
		returnErr = errors.Join(returnErr, r.save(reportPath))
	}()
	if cleanupOnly {
		return nil
	}
	var ca []byte
	if caFile != "" {
		ca, err = os.ReadFile(caFile)
		if err != nil {
			return err
		}
		if err := validateCA(ca); err != nil {
			return err
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	r.Checks = append(r.Checks, check{Name: "opencyber-runtime-template", Status: "incomplete", Detail: "route preflight only; pinned runtime tools, export and full lifecycle matrix remain unqualified"})
	fixture, err := os.ReadFile(fixturePath)
	if err != nil {
		return err
	}
	probe, err := assets.ReadFile("assets/probe.py")
	if err != nil {
		return err
	}
	r.Hashes["target.py"], r.Hashes["probe.py"] = digest(fixture), digest(probe)
	if exe, err := os.Executable(); err == nil {
		if body, err := os.ReadFile(exe); err == nil {
			r.Hashes["driver"] = digest(body)
		}
	}
	create := func(role string) (*cubesandbox.Sandbox, error) {
		no := false
		template := cfg.TemplateID
		if role == "assessor" && assessmentTemplate != "" {
			template = assessmentTemplate
		}
		if role == "assessor" {
			r.AssessorTemplate = template
		}
		opts := cube.CompatibleCreateOptions(cubesandbox.CreateOptions{TemplateID: template, Timeout: cubesandbox.DurationPtr(5 * time.Minute),
			AllowInternetAccess: &no, Network: cubesandbox.NetworkOptions{AllowPublicTraffic: &no},
			Metadata: map[string]string{"esf.security.owner": r.Owner, "esf.security.role": role}}, cfg.Version)
		r.PendingAllocation = true
		if err := r.save(reportPath); err != nil {
			return nil, err
		}
		sb, err := client.Create(ctx, opts)
		if err != nil {
			return nil, err
		}
		r.Owned = append(r.Owned, sb.SandboxID)
		r.PendingAllocation = false
		if err := r.save(reportPath); err != nil {
			return nil, err
		}
		if sb.TrafficAccessToken == "" {
			return nil, errors.New("restricted ingress token missing")
		}
		return sb, nil
	}
	target, err := create("target")
	if err != nil {
		r.Checks = append(r.Checks, check{Name: "target-allocation", Status: "incomplete"})
		return errors.New("target allocation failed; inspect private receipt and reconcile")
	}
	assessor, err := create("assessor")
	if err != nil {
		r.Checks = append(r.Checks, check{Name: "assessor-allocation", Status: "incomplete"})
		return errors.New("assessor allocation failed; inspect private receipt and reconcile")
	}
	if err := target.Files().Write(ctx, "/tmp/security-target.py", fixture); err != nil {
		return errors.New("target transfer failed")
	}
	if err := assessor.Files().Write(ctx, "/tmp/security-probe.py", probe); err != nil {
		return errors.New("probe transfer failed")
	}
	appToken := nonce()
	targetURL := "https://" + target.GetHost(18080)
	wrongPortURL := "https://" + target.GetHost(18081)
	env := map[string]string{"TARGET_BEARER_TOKEN": appToken, "TARGET_RUN_ID": r.Owner, "TARGET_INSTANCE_ID": target.SandboxID, "TARGET_REDIRECT_URL": wrongPortURL + "/health"}
	for _, port := range []string{"18080", "18081"} {
		command := "nohup python3 /tmp/security-target.py --port " + port + " --log /tmp/target-" + port + ".jsonl >/tmp/start-" + port + ".log 2>&1 </dev/null &"
		out, err := target.Commands().Run(ctx, command, cubesandbox.CommandOptions{Envs: env, Timeout: 10 * time.Second})
		if err != nil || out.ExitCode != 0 {
			return errors.New("synthetic target startup failed")
		}
	}
	pool, err := x509.SystemCertPool()
	if err != nil {
		return err
	}
	if len(ca) > 0 && !pool.AppendCertsFromPEM(ca) {
		return errors.New("preview CA not loaded")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{Timeout: 4 * time.Second}).DialContext(ctx, network, net.JoinHostPort(cfg.ProxyNodeIP, fmt.Sprint(cfg.ProxyPortHTTP)))
	}
	defer transport.CloseIdleConnections()
	upstream := &http.Client{Transport: transport, Timeout: 5 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	for index, base := range []string{targetURL, wrongPortURL} {
		cid := r.Owner + fmt.Sprintf("-ready-%d", index)
		var ready bool
		for attempt := 0; attempt < 5; attempt++ {
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/health", nil)
			req.Header.Set("cube-traffic-access-token", target.TrafficAccessToken)
			req.Header.Set("Authorization", "Bearer "+appToken)
			req.Header.Set("X-Correlation-ID", cid)
			response, err := upstream.Do(req)
			if err == nil {
				body, readErr := io.ReadAll(io.LimitReader(response.Body, 8193))
				response.Body.Close()
				var identity struct {
					Run      string `json:"run_id"`
					Instance string `json:"instance_id"`
				}
				ready = readErr == nil && len(body) <= 8192 && response.StatusCode == 200 && json.Unmarshal(body, &identity) == nil && identity.Run == r.Owner && identity.Instance == target.SandboxID && response.Header.Get("X-Correlation-ID") == cid
			}
			if ready {
				break
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(200 * time.Millisecond):
			}
		}
		if !ready {
			r.Checks = append(r.Checks, check{Name: fmt.Sprintf("readiness-%d", index), Status: "incomplete"})
			return errors.New("target readiness positive control unavailable")
		}
		r.Checks = append(r.Checks, check{Name: fmt.Sprintf("readiness-%d", index), Status: "pass", Detail: "factory-side TLS-verified target/instance/correlation positive control"})
	}
	guard := &route.HostGuard{Owner: r.Owner, IP: fmt.Sprintf("192.0.2.%d", 32+int(sha256.Sum256([]byte(r.Owner))[0])%192), Port: 9445}
	guard.Socket = guardSocket
	r.RouteGuard = guard
	if err := r.save(reportPath); err != nil {
		return err
	}
	if err := guard.Open(ctx); err != nil {
		r.Checks = append(r.Checks, check{Name: "route-host-guard", Status: "incomplete"})
		return err
	}
	capability := nonce()
	broker, err = route.Start(route.Config{ListenAddress: net.JoinHostPort(guard.IP, fmt.Sprint(guard.Port)), Hostname: "assessment-" + strings.TrimPrefix(r.Owner, "qualification-") + ".esf.invalid",
		TargetURL: targetURL, TargetTrafficToken: target.TrafficAccessToken, AccessToken: capability, UpstreamClient: upstream,
		AllowedPaths: []string{"/health", "/api/resources/a", "/api/resources/b", "/redirect"}, MaxRequests: 100, MaxDuration: 15 * time.Minute, MaxResponseBytes: 8192})
	if err != nil {
		return err
	}
	ca = broker.PublicCA()
	if err := assessor.Files().Write(ctx, "/tmp/route-ca.pem", ca); err != nil {
		return err
	}
	r.Hashes["endpoint-ca.pem"] = digest(ca)
	payload := map[string]string{"target_url": broker.URL(), "forbidden_port_url": wrongPortURL, "proxy_ip": guard.IP, "proxy_port": fmt.Sprint(guard.Port),
		"endpoint_token": capability, "app_token": appToken, "owner": r.Owner, "target_id": target.SandboxID, "ca_file": "/tmp/route-ca.pem",
		"management_ip": cfg.ProxyNodeIP, "management_port": "4000"}
	encoded, _ := json.Marshal(payload)
	if err := assessor.Files().Write(ctx, "/tmp/route-input.json", encoded); err != nil {
		return errors.New("private probe input transfer failed")
	}
	no := false
	if err := assessor.UpdateNetwork(ctx, cubesandbox.UpdateNetworkOptions{AllowInternetAccess: &no,
		NetworkOptions: cubesandbox.NetworkOptions{AllowPublicTraffic: &no, AllowOut: []string{guard.IP}}}); err != nil {
		return errors.New("assessment network policy failed")
	}
	controlChecks, controlErr := qualifyGuard(ctx, *guard, assessor, r.Owner)
	r.Checks = append(r.Checks, controlChecks...)
	if controlErr != nil {
		return controlErr
	}
	out, err := assessor.Commands().Run(ctx, "python3 /tmp/security-probe.py /tmp/route-input.json", cubesandbox.CommandOptions{Timeout: 75 * time.Second})
	if err != nil {
		return errors.New("route probe transport failed")
	}
	var result struct {
		Checks []check `json:"checks"`
	}
	if len(out.Stdout) > 65536 || json.Unmarshal([]byte(out.Stdout), &result) != nil || len(result.Checks) == 0 {
		return errors.New("missing, truncated or malformed probe evidence")
	}
	r.Checks = append(r.Checks, result.Checks...)
	artifact := reportPath + ".probe.json"
	f, err := os.OpenFile(artifact, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		r.Checks = append(r.Checks, check{Name: "evidence-retention", Status: "incomplete"})
		return errors.New("cannot retain private probe evidence")
	}
	if err := handoffOwnership(f); err != nil {
		f.Close()
		return err
	}
	_, writeErr := f.WriteString(out.Stdout)
	syncErr := f.Sync()
	closeErr := f.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		r.Checks = append(r.Checks, check{Name: "evidence-retention", Status: "incomplete"})
		return errors.New("private probe evidence incomplete")
	}
	r.Hashes[filepath.Base(artifact)] = digest([]byte(out.Stdout))
	if out.ExitCode != 0 {
		return errors.New("route qualification failed; integration remains disabled (see receipt)")
	}
	// The target has no assessor capability. Prove distinct filesystems and
	// denied target-origin connections, with factory-side reachable controls.
	for _, port := range []int{4000, 9443} {
		control, err := (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, "tcp", net.JoinHostPort(cfg.ProxyNodeIP, strconv.Itoa(port)))
		if err != nil {
			r.Checks = append(r.Checks, check{Name: fmt.Sprintf("target-isolation-control-%d", port), Status: "incomplete", Detail: "same destination TCP listener positive control unavailable"})
			return errors.New("target isolation positive control unavailable")
		}
		control.Close()
		r.Checks = append(r.Checks, check{Name: fmt.Sprintf("target-isolation-control-%d", port), Status: "pass", Detail: "factory reached exact target-origin forbidden destination tuple"})
	}
	sentinel := []byte(nonce())
	if err := assessor.Files().Write(ctx, "/tmp/isolation-sentinel", sentinel); err != nil {
		return errors.New("assessor write positive control unavailable")
	}
	isolationProbe, _ := isolationAssets.ReadFile("assets/isolation_probe.py")
	r.Hashes["isolation_probe.py"] = digest(isolationProbe)
	if err := target.Files().Write(ctx, "/tmp/isolation-probe.py", isolationProbe); err != nil {
		return err
	}
	isolation, err := target.Commands().Run(ctx, "python3 /tmp/isolation-probe.py "+cfg.ProxyNodeIP+" "+guard.IP, cubesandbox.CommandOptions{Timeout: 15 * time.Second})
	if err != nil || isolation == nil || len(isolation.Stdout) > 8192 {
		return errors.New("target isolation probe unavailable")
	}
	var isolationResult struct {
		Checks []check `json:"checks"`
	}
	if json.Unmarshal([]byte(isolation.Stdout), &isolationResult) != nil || len(isolationResult.Checks) != 3 {
		return errors.New("target isolation probe malformed")
	}
	r.Checks = append(r.Checks, isolationResult.Checks...)
	readRequest, _ := http.NewRequestWithContext(ctx, http.MethodGet, cfg.ProxyScheme+"://"+assessor.GetHost(49983)+"/files?path=/tmp/isolation-sentinel&username=root", nil)
	readRequest.Header.Set("cube-traffic-access-token", assessor.TrafficAccessToken)
	readRequest.Header.Set("X-Access-Token", assessor.EnvdAccessToken)
	readResponse, readErr := upstream.Do(readRequest)
	var after []byte
	if readErr == nil {
		if readResponse.StatusCode == http.StatusOK {
			after, readErr = io.ReadAll(io.LimitReader(readResponse.Body, int64(len(sentinel)+1)))
		}
		readResponse.Body.Close()
	}
	if readErr != nil || digest(after) != digest(sentinel) {
		r.Checks = append(r.Checks, check{Name: "assessor-filesystem-isolation", Status: "fail"})
		return errors.New("assessor sentinel changed or missing")
	}
	r.Hashes["assessor-isolation-sentinel"] = digest(sentinel)
	r.Checks = append(r.Checks, check{Name: "assessor-filesystem-isolation", Status: "pass", Detail: "target local write left factory-held assessor sentinel hash unchanged"})
	if isolation.ExitCode != 0 {
		return errors.New("target isolation denied qualification")
	}
	r.RouteQualified = true
	return nil
}

func handoffOwnership(file *os.File) error {
	if os.Geteuid() != 0 || os.Getenv("SUDO_UID") == "" {
		return nil
	}
	uid, err := strconv.Atoi(os.Getenv("SUDO_UID"))
	if err != nil || uid < 0 {
		return errors.New("invalid invoking user identity")
	}
	gid, err := strconv.Atoi(os.Getenv("SUDO_GID"))
	if err != nil || gid < 0 {
		return errors.New("invalid invoking group identity")
	}
	return file.Chown(uid, gid)
}

func qualifyGuard(ctx context.Context, g route.HostGuard, assessor *cubesandbox.Sandbox, owner string) ([]check, error) {
	tcpListener, err := net.Listen("tcp", net.JoinHostPort(g.IP, fmt.Sprint(g.Port+1)))
	if err != nil {
		return nil, err
	}
	defer tcpListener.Close()
	udpListener, err := net.ListenPacket("udp4", net.JoinHostPort(g.IP, fmt.Sprint(g.Port)))
	if err != nil {
		return nil, err
	}
	defer udpListener.Close()
	go func() {
		for {
			connection, err := tcpListener.Accept()
			if err != nil {
				return
			}
			connection.SetDeadline(time.Now().Add(3 * time.Second))
			expected := owner + "-tcp-control"
			body := make([]byte, len(expected))
			if _, err := io.ReadFull(connection, body); err == nil && string(body) == expected {
				connection.Write(body)
			}
			connection.Close()
		}
	}()
	go func() {
		for {
			body := make([]byte, 512)
			size, peer, err := udpListener.ReadFrom(body)
			if err != nil {
				return
			}
			if string(body[:size]) == owner+"-udp-control" {
				udpListener.WriteTo(body[:size], peer)
			}
		}
	}()
	var checks []check
	for _, protocol := range []string{"tcp", "udp"} {
		if err := g.SetControl(ctx, protocol, true); err != nil {
			return checks, err
		}
		probe := func() (bool, bool, int, error) {
			out, err := assessor.Commands().Run(ctx, "python3 /tmp/security-probe.py /tmp/route-input.json "+protocol, cubesandbox.CommandOptions{Timeout: 8 * time.Second})
			if err != nil {
				return false, false, 0, err
			}
			var result struct {
				Protocol    string `json:"protocol"`
				Correlation string `json:"correlation"`
				Reachable   bool   `json:"reachable"`
				Connected   bool   `json:"connected"`
				Errno       int    `json:"errno"`
			}
			if out.ExitCode != 0 || len(out.Stdout) > 4096 || json.Unmarshal([]byte(out.Stdout), &result) != nil || result.Protocol != protocol || result.Correlation != owner+"-"+protocol+"-control" {
				return false, false, 0, errors.New("invalid matched network control evidence")
			}
			return result.Reachable, result.Connected, result.Errno, nil
		}
		reachable, _, _, positiveErr := probe()
		removeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		removeErr := g.SetControl(removeCtx, protocol, false)
		cancel()
		if positiveErr != nil || !reachable || removeErr != nil {
			return checks, errors.Join(positiveErr, removeErr, errors.New("network positive control unavailable"))
		}
		checks = append(checks, check{Name: protocol + "-reachable-control", Status: "pass", Detail: owner + "-" + protocol + "-control"})
		var before, after uint64
		if protocol == "udp" {
			before, err = g.UDPRejectCount(ctx)
			if err != nil {
				return checks, err
			}
		}
		reachable, connected, errno, negativeErr := probe()
		if negativeErr != nil {
			return checks, negativeErr
		}
		denied := !reachable && !connected
		if protocol == "udp" {
			after, err = g.UDPRejectCount(ctx)
			if err != nil {
				return checks, err
			}
			denied = !reachable && after > before
		}
		status := "fail"
		if denied {
			status = "pass"
		}
		checks = append(checks, check{Name: protocol + "-guard-denial", Status: status, Detail: fmt.Sprintf("same live listener/correlation: reachable=%t connected=%t errno=%d matched_udp_rejects=%d->%d", reachable, connected, errno, before, after)})
		if !denied {
			return checks, errors.New("owned endpoint protocol/port guard did not enforce denial")
		}
	}
	return checks, nil
}

func validateCA(data []byte) error {
	if len(data) == 0 || len(data) > 128*1024 {
		return errors.New("CA input is empty or oversized")
	}
	count := 0
	for len(strings.TrimSpace(string(data))) > 0 {
		block, rest := pem.Decode(data)
		if block == nil || block.Type != "CERTIFICATE" {
			return errors.New("CA input must contain only public certificates")
		}
		certificate, err := x509.ParseCertificate(block.Bytes)
		if err != nil || !certificate.IsCA {
			return errors.New("CA input contains an invalid CA certificate")
		}
		data = rest
		count++
	}
	if count == 0 {
		return errors.New("CA input contains no public certificates")
	}
	return nil
}

func validOwner(owner string) bool {
	suffix, ok := strings.CutPrefix(owner, "qualification-")
	if !ok || len(suffix) != 32 {
		return false
	}
	_, err := hex.DecodeString(suffix)
	return err == nil
}

func reconcile(ctx context.Context, client *cubesandbox.Client, cfg cube.Config, r *report) error {
	r.Remaining = append([]string(nil), r.Owned...)
	infos, err := client.List(ctx)
	if err != nil {
		r.Checks = append(r.Checks, check{Name: "cleanup", Status: "incomplete"})
		return errors.New("cannot list resources for reconciliation")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	defer transport.CloseIdleConnections()
	deleter := &http.Client{Transport: transport, Timeout: 10 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	var failures error
	for _, id := range ownedIDs(infos, r.Owner) {
		req, err := http.NewRequestWithContext(ctx, http.MethodDelete, strings.TrimRight(cfg.APIURL, "/")+"/sandboxes/"+url.PathEscape(id), nil)
		if err != nil {
			failures = errors.Join(failures, errors.New("invalid cleanup request"))
			continue
		}
		if cfg.APIKey != "" {
			req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
		}
		response, err := deleter.Do(req)
		if err != nil {
			failures = errors.Join(failures, errors.New("owned sandbox deletion unavailable"))
			continue
		}
		response.Body.Close()
		if response.StatusCode != 200 && response.StatusCode != 204 && response.StatusCode != 404 {
			failures = errors.Join(failures, errors.New("owned sandbox deletion failed"))
		}
	}
	infos, err = client.List(ctx)
	if err != nil {
		r.Checks = append(r.Checks, check{Name: "cleanup", Status: "incomplete"})
		return errors.Join(failures, errors.New("cleanup verification unavailable"))
	}
	r.Remaining = ownedIDs(infos, r.Owner)
	r.Unrelated = nil
	for _, info := range infos {
		if info.Metadata["esf.security.owner"] != r.Owner {
			r.Unrelated = append(r.Unrelated, info.SandboxID)
		}
	}
	if len(r.Remaining) > 0 || failures != nil {
		r.Checks = append(r.Checks, check{Name: "cleanup", Status: "fail"})
		return errors.Join(failures, errors.New("owned sandbox cleanup not established"))
	}
	if r.PendingAllocation {
		r.Checks = append(r.Checks, check{Name: "cleanup", Status: "incomplete", Detail: "allocation acknowledgement missing; late resources may appear; reconcile after control-plane settlement"})
		return errors.New("allocation outcome unknown; cleanup remains incomplete")
	}
	r.Checks = append(r.Checks, check{Name: "cleanup", Status: "pass", Detail: "zero owned sandboxes; unrelated resources untouched"})
	return nil
}
