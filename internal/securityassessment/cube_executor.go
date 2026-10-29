package securityassessment

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mitkox/esf/internal/sandbox/cube"
	"github.com/mitkox/esf/internal/securityassessment/route"
	cubesandbox "github.com/tencentcloud/CubeSandbox/sdk/go"
)

// CubeExecutorConfig is trusted factory configuration, never assessment input.
// Only public CA bytes, the driver, the candidate, and per-attempt synthetic
// capabilities cross into VMs. Cube control credentials stay in this process.
type CubeExecutorConfig struct {
	Cube                                                          cube.Config
	GuardSocket, DriverBinaryPath, DriverSHA256, TargetEntrypoint string
	PrivateJournalRoot, PreviewCAFile, RuntimeInventorySHA256     string
}

type CubeAssessmentRequest struct {
	RunID, ChangeID, ParentRunID string
	Profile                      Profile
	Candidate                    CandidateExport
}

type CubeAssessmentResult struct {
	Profile         Profile
	Expected        Identity
	Manifest        Manifest
	PrivateEvidence map[string][]byte
	ExpectedHashes  map[string]string
	Duration        time.Duration
	Requests        uint64
	JournalPath     string
}

type CubeExecutor struct {
	config CubeExecutorConfig
	driver []byte
}

func NewCubeExecutor(config CubeExecutorConfig) (*CubeExecutor, error) {
	if err := config.Cube.Validate(); err != nil {
		return nil, err
	}
	if config.Cube.ProxyNodeIP == "" || config.Cube.ProxyPortHTTP <= 0 || config.Cube.ProxyScheme != "https" ||
		strings.TrimPrefix(config.Cube.Version, "v") != "0.7.3" ||
		!filepath.IsAbs(config.GuardSocket) || !filepath.IsAbs(config.PrivateJournalRoot) || !filepath.IsAbs(config.DriverBinaryPath) ||
		!digestPattern.MatchString(config.DriverSHA256) || !digestPattern.MatchString(config.RuntimeInventorySHA256) ||
		!safeEvidenceName(config.TargetEntrypoint) || strings.Contains(config.TargetEntrypoint, "'") || !strings.HasSuffix(config.TargetEntrypoint, ".py") {
		return nil, errors.New("invalid trusted Cube assessment configuration")
	}
	f, err := os.Open(config.DriverBinaryPath)
	if err != nil {
		return nil, errors.New("pinned assessment driver unavailable")
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, (32<<20)+1))
	if err != nil || len(data) == 0 || len(data) > 32<<20 || SHA256(data) != config.DriverSHA256 {
		return nil, errors.New("assessment driver pin mismatch")
	}
	return &CubeExecutor{config: config, driver: data}, nil
}

type cubeAttemptJournal struct {
	Schema      string          `json:"schema"`
	Owner       string          `json:"owner"`
	RunID       string          `json:"run_id"`
	IDs         []string        `json:"sandbox_ids"`
	Pending     bool            `json:"pending_allocation"`
	Guard       route.HostGuard `json:"guard"`
	GuardIntent bool            `json:"guard_intent"`
	Cleanup     Cleanup         `json:"cleanup"`
	ErrorCode   string          `json:"error_code,omitempty"`
}

func writeCubeJournal(name string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(name+".new", os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	_, a := f.Write(data)
	b := f.Sync()
	c := f.Close()
	if err := errors.Join(a, b, c); err != nil {
		return err
	}
	if err := os.Rename(name+".new", name); err != nil {
		return err
	}
	d, err := os.Open(filepath.Dir(name))
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

func randomCubeToken() (string, error) {
	var b [16]byte
	_, err := rand.Read(b[:])
	return hex.EncodeToString(b[:]), err
}

// Assess creates two disposable VMs. No target execution or HTTP assessment is
// retried; only readiness/result checkpoints are polled. A lost create response
// remains an unknown allocation in the journal even if inventory appears empty.
func (e *CubeExecutor) Assess(parent context.Context, in CubeAssessmentRequest) (out CubeAssessmentResult, retErr error) {
	started := time.Now()
	if in.ChangeID == "" {
		in.ChangeID = in.RunID
	}
	if !identifierPattern.MatchString(in.RunID) || !identifierPattern.MatchString(in.ChangeID) || (in.ParentRunID != "" && !identifierPattern.MatchString(in.ParentRunID)) {
		return out, errors.New("invalid assessment identity")
	}
	if err := in.Profile.Validate(); err != nil {
		return out, err
	}
	validated, err := candidateFromArchive(in.Candidate.Manifest, in.Candidate.Archive, DefaultCandidatePolicy())
	if err != nil || validated.Manifest.Digest != in.Candidate.Manifest.Digest || validated.Manifest.ArchiveSHA256 != in.Candidate.Manifest.ArchiveSHA256 {
		return out, errors.New("candidate archive identity mismatch")
	}
	if _, ok := validated.Files[e.config.TargetEntrypoint]; !ok {
		return out, errors.New("approved candidate target entrypoint absent")
	}
	if err := route.CheckHostGuardSupport(); err != nil {
		return out, err
	}
	ctx, cancel := context.WithTimeout(parent, in.Profile.Limits.MaxDuration)
	defer cancel()
	token, err := randomCubeToken()
	if err != nil {
		return out, err
	}
	owner := "qualification-" + token
	root := filepath.Join(e.config.PrivateJournalRoot, in.RunID+"-"+token)
	if err = os.MkdirAll(e.config.PrivateJournalRoot, 0700); err != nil {
		return out, err
	}
	if err = os.Mkdir(root, 0700); err != nil {
		return out, err
	}
	out.JournalPath = filepath.Join(root, "ownership.json")
	ipByte, _ := strconv.ParseUint(token[:2], 16, 8)
	journal := cubeAttemptJournal{Schema: "esf.assessment-ownership/v1", Owner: owner, RunID: in.RunID,
		Guard: route.HostGuard{Owner: owner, IP: fmt.Sprintf("192.0.2.%d", 1+ipByte%254), Port: 9445, Socket: e.config.GuardSocket}}
	if err = writeCubeJournal(out.JournalPath, journal); err != nil {
		return out, err
	}
	cfg := e.config.Cube
	client := cubesandbox.NewClient(cubesandbox.Config{APIURL: cfg.APIURL, APIKey: cfg.APIKey, TemplateID: cfg.TemplateID, ProxyNodeIP: cfg.ProxyNodeIP, ProxyPortHTTP: cfg.ProxyPortHTTP, ProxyScheme: cfg.ProxyScheme, SandboxDomain: cfg.SandboxDomain, RequestTimeout: 30 * time.Second})
	defer client.Close()
	control := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 30 * time.Second, CheckRedirect: denyCubeRedirect}
	defer control.CloseIdleConnections()
	var broker *route.Broker
	var healthRequests uint64
	defer func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 90*time.Second)
		defer stop()
		var closeErr error
		if broker != nil {
			out.Requests = broker.Stats().Requests
			closeErr = broker.Close(cleanupCtx)
		}
		out.Requests += healthRequests
		cleanup, cleanupErr := cleanupCubeOwned(cleanupCtx, client, control, cfg, owner, journal.IDs, journal.Pending)
		if journal.GuardIntent {
			closeErr = errors.Join(closeErr, journal.Guard.Close(cleanupCtx))
		}
		if closeErr != nil {
			cleanup.Status = Incomplete
		}
		journal.Cleanup = cleanup
		if retErr != nil {
			journal.ErrorCode = "assessment_execution_incomplete"
		}
		journalErr := writeCubeJournal(out.JournalPath, journal)
		out.Manifest.Cleanup = cleanup
		if out.Manifest.Schema != "" {
			verifyErr := out.Manifest.Verify(out.Expected, out.PrivateEvidence, out.ExpectedHashes)
			retErr = errors.Join(retErr, verifyErr)
			journalErr = errors.Join(journalErr, writeCubeJournal(filepath.Join(root, "manifest.json"), out.Manifest))
		}
		out.Duration = time.Since(started)
		if cleanupErr != nil || closeErr != nil || journalErr != nil {
			retErr = errors.Join(retErr, errors.New("assessment cleanup incomplete; retain ownership journal"))
		}
	}()
	// This defer runs before detached cleanup. Cancellation that occurs only
	// while cleanup runs must not recategorize an earlier unrelated failure.
	defer func() { retErr = preserveCubeFailureContext(ctx, retErr) }()
	// Check live immutable runtime metadata before allocating either VM. Local
	// ext4 templates expose the artifact hash under createRequest, which the
	// SDK's TemplateInfo omits; an OCI build receipt alone cannot bind that path.
	if err = verifyCubeRuntimeTemplate(ctx, control, cfg, in.Profile.Runtime); err != nil {
		return out, errors.New("assessment runtime template pin unavailable")
	}
	create := func(template string) (*cubesandbox.Sandbox, error) {
		journal.Pending = true
		if err := writeCubeJournal(out.JournalPath, journal); err != nil {
			return nil, err
		}
		deny := false
		opts := cubesandbox.CreateOptions{TemplateID: template, Timeout: cubesandbox.DurationPtr(in.Profile.Limits.MaxDuration + time.Minute), Metadata: map[string]string{"esf.security.owner": owner, "esf.security.run": in.RunID}, AllowInternetAccess: &deny, Network: cubesandbox.NetworkOptions{AllowPublicTraffic: &deny}}
		sb, err := client.Create(ctx, cube.CompatibleCreateOptions(opts, cfg.Version))
		if err != nil {
			return nil, errors.New("Cube allocation acknowledgement unavailable")
		}
		if sb == nil || !identifierPattern.MatchString(sb.SandboxID) {
			return nil, errors.New("invalid Cube allocation acknowledgement")
		}
		journal.IDs = append(journal.IDs, sb.SandboxID)
		journal.Pending = false
		if err := writeCubeJournal(out.JournalPath, journal); err != nil {
			return nil, err
		}
		if sb.TrafficAccessToken == "" {
			return nil, errors.New("private Cube ingress capability unavailable")
		}
		return sb, nil
	}
	target, err := create(cfg.TemplateID)
	if err != nil {
		return out, err
	}
	assessor, err := create(in.Profile.Runtime.TemplateID)
	if err != nil {
		return out, err
	}
	dataClient, err := e.dataClient()
	if err != nil {
		return out, err
	}
	defer dataClient.CloseIdleConnections()
	readFile := func(sb *cubesandbox.Sandbox, name string, max int64) ([]byte, error) {
		return readBoundedHTTP(ctx, dataClient, cfg.ProxyScheme+"://"+sb.GetHost(49983)+"/files?path="+url.QueryEscape(name)+"&username=root", cubePrivateHeaders(sb), max)
	}
	inventory, err := readFile(assessor, "/opt/security-runtime/runtime.json", 1<<20)
	if err != nil || SHA256(inventory) != e.config.RuntimeInventorySHA256 {
		return out, errors.New("assessment runtime inventory pin mismatch")
	}
	if err = target.Files().Write(ctx, "/tmp/candidate.tar", validated.Archive); err != nil {
		return out, errors.New("candidate transfer incomplete")
	}
	run := func(stage string, sb *cubesandbox.Sandbox, command string, opts cubesandbox.CommandOptions) error {
		result, err := sb.Commands().Run(ctx, command, opts)
		if err != nil || result == nil || result.ExitCode != 0 {
			status := map[string]any{"stage": stage, "transport_error": err != nil}
			if err != nil {
				detail := err.Error()
				for _, secret := range []string{sb.EnvdAccessToken, sb.TrafficAccessToken, cfg.APIKey} {
					if secret != "" {
						detail = strings.ReplaceAll(detail, secret, "[redacted]")
					}
				}
				status["transport_detail"] = detail[:min(len(detail), 4096)]
			}
			if result != nil {
				status["exit"] = result.ExitCode
				status["stdout"] = result.Stdout[:min(len(result.Stdout), 4096)]
				status["stderr"] = result.Stderr[:min(len(result.Stderr), 4096)]
			}
			_ = writeCubeJournal(filepath.Join(root, "setup-failure.json"), status)
			return fmt.Errorf("Cube setup incomplete at %s", stage)
		}
		return nil
	}
	setup := "printf '%s  /tmp/candidate.tar\\n' '" + validated.Manifest.ArchiveSHA256 + "' | sha256sum -c - >/dev/null && mkdir -m 0755 /tmp/target-app && tar --no-same-owner -xf /tmp/candidate.tar -C /tmp/target-app && chmod -R a+rX /tmp/target-app && rm /tmp/candidate.tar"
	if err = run("candidate-extract", target, setup, cubesandbox.CommandOptions{Timeout: 30 * time.Second}); err != nil {
		return out, err
	}
	appToken, err := randomCubeToken()
	if err != nil {
		return out, err
	}
	capToken, err := randomCubeToken()
	if err != nil {
		return out, err
	}
	authorization := "Bearer " + appToken
	startTarget := "nohup python3 '/tmp/target-app/" + e.config.TargetEntrypoint + "' --port 18080 --log /tmp/target-api-access.jsonl --max-log-bytes " + strconv.FormatInt(in.Profile.Limits.MaxLogBytes, 10) + " >/tmp/target-start.log 2>&1 </dev/null &"
	if err = run("target-start", target, startTarget, cubesandbox.CommandOptions{User: "nobody", Cwd: "/tmp/target-app", Timeout: 10 * time.Second, Envs: map[string]string{"TARGET_BEARER_TOKEN": appToken, "TARGET_RUN_ID": in.RunID, "TARGET_INSTANCE_ID": target.SandboxID}}); err != nil {
		return out, err
	}
	targetURL := cfg.ProxyScheme + "://" + target.GetHost(18080)
	health := func() (bool, error) {
		if healthRequests >= uint64(in.Profile.Limits.MaxRequests) {
			return false, errors.New("target request budget exhausted")
		}
		healthRequests++
		headers := cubeTrafficHeaders(target)
		headers.Set("Authorization", authorization)
		headers.Set("X-Correlation-ID", "factory-health")
		data, err := readBoundedHTTP(ctx, dataClient, targetURL+"/health", headers, 8192)
		if err != nil {
			return false, err
		}
		var value struct {
			Ready       bool   `json:"ready"`
			RunID       string `json:"run_id"`
			InstanceID  string `json:"instance_id"`
			LogComplete bool   `json:"log_complete"`
		}
		if json.Unmarshal(data, &value) != nil || !value.Ready || value.RunID != in.RunID || value.InstanceID != target.SandboxID {
			return false, errors.New("target readiness identity mismatch")
		}
		return value.LogComplete, nil
	}
	readyDeadline := time.Now().Add(30 * time.Second)
	for {
		complete, healthErr := health()
		if healthErr == nil && complete {
			break
		}
		if time.Now().After(readyDeadline) || healthRequests >= 10 || healthRequests+3 >= uint64(in.Profile.Limits.MaxRequests) {
			return out, errors.New("candidate target did not become ready")
		}
		if err = waitCubePoll(ctx); err != nil {
			return out, err
		}
	}
	journal.GuardIntent = true
	if err = writeCubeJournal(out.JournalPath, journal); err != nil {
		return out, err
	}
	if err = journal.Guard.Open(ctx); err != nil {
		return out, errors.New("exclusive endpoint guard unavailable")
	}
	hostname := "assessment-" + token + ".esf.invalid"
	// Reserve the final log-completeness health read, and charge readiness
	// attempts to the same request ceiling as the fixed MCP workload.
	brokerBudget := in.Profile.Limits.MaxRequests - int(healthRequests) - 1
	if brokerBudget < 2 {
		return out, errors.New("fixed assessment request budget unavailable")
	}
	broker, err = route.Start(route.Config{ListenAddress: net.JoinHostPort(journal.Guard.IP, "9445"), Hostname: hostname, AccessToken: capToken, TargetURL: targetURL, TargetTrafficToken: target.TrafficAccessToken, UpstreamClient: dataClient, AllowedPaths: []string{"/health", "/api/resources/a", "/api/resources/b", "/redirect"}, MaxRequests: brokerBudget, MaxDuration: in.Profile.Limits.MaxDuration, MaxResponseBytes: 8192})
	if err != nil {
		return out, errors.New("exclusive endpoint broker unavailable")
	}
	deny := false
	if err = assessor.UpdateNetwork(ctx, cubesandbox.UpdateNetworkOptions{AllowInternetAccess: &deny, NetworkOptions: cubesandbox.NetworkOptions{AllowPublicTraffic: &deny, AllowOut: []string{journal.Guard.IP + "/32"}}}); err != nil {
		return out, errors.New("assessor endpoint policy unavailable")
	}
	for name, data := range map[string][]byte{"/tmp/security-assessment": e.driver, "/tmp/route-ca.pem": broker.PublicCA()} {
		if err = assessor.Files().Write(ctx, name, data); err != nil {
			return out, errors.New("assessor trusted input transfer incomplete")
		}
	}
	if err = run("assessor-trust", assessor, "chmod 0700 /tmp/security-assessment", cubesandbox.CommandOptions{Timeout: 10 * time.Second}); err != nil {
		return out, err
	}
	out.Profile = in.Profile
	out.Profile.TargetURL = broker.URL()
	profileDigest, err := out.Profile.Digest()
	if err != nil {
		return out, err
	}
	requestDigest, err := FixedRequestDigest(out.Profile, in.RunID, target.SandboxID, authorization, capToken)
	if err != nil {
		return out, err
	}
	out.Expected = Identity{RunID: in.RunID, ChangeID: in.ChangeID, ParentRunID: in.ParentRunID, CandidateDigest: validated.Manifest.Digest, BuiltArtifactDigest: validated.Manifest.ArchiveSHA256, TargetInstanceID: target.SandboxID, ProfileDigest: profileDigest, RequestDigest: requestDigest, RequiredChecks: out.Profile.RequiredChecks, Limits: out.Profile.Limits, AI: out.Profile.AI, Runtime: out.Profile.Runtime}
	input := RunInput{Schema: AssessmentInputSchema, Profile: out.Profile, Identity: out.Expected, Authorization: authorization, EndpointToken: capToken}
	inputBytes, err := json.Marshal(input)
	if err != nil {
		return out, err
	}
	if err = assessor.Files().Write(ctx, "/tmp/assessment-input.json", inputBytes); err != nil {
		return out, errors.New("private assessor input transfer incomplete")
	}
	if err = run("assessor-input-permissions", assessor, "chmod 0600 /tmp/assessment-input.json", cubesandbox.CommandOptions{Timeout: 10 * time.Second}); err != nil {
		return out, err
	}
	if err = run("assessor-start", assessor, "nohup /tmp/security-assessment --input /tmp/assessment-input.json --output /tmp/assessment-output --endpoint-address '"+journal.Guard.IP+"' >/tmp/assessment-driver.log 2>&1 </dev/null &", cubesandbox.CommandOptions{Timeout: 10 * time.Second}); err != nil {
		return out, err
	}
	for {
		data, readErr := readFile(assessor, "/tmp/assessment-output/ready.json", 8192)
		if readErr == nil {
			var checkpoint struct {
				Schema      string `json:"schema"`
				RunID       string `json:"run_id"`
				Target      string `json:"target_instance_id"`
				Profile     string `json:"profile_digest"`
				Request     string `json:"request_digest"`
				Correlation string `json:"isolation_correlation"`
			}
			if strictCubeJSON(data, &checkpoint) != nil || checkpoint.Schema != "esf.fixed-assessment-ready/v1" || checkpoint.RunID != in.RunID || checkpoint.Target != target.SandboxID || checkpoint.Profile != profileDigest || checkpoint.Request != requestDigest || !identifierPattern.MatchString(checkpoint.Correlation) {
				return out, errors.New("assessor checkpoint identity mismatch")
			}
			break
		}
		if !errors.Is(readErr, os.ErrNotExist) {
			return out, errors.New("assessor checkpoint unavailable")
		}
		if _, failureErr := readFile(assessor, "/tmp/assessment-output/incomplete.json", 8192); failureErr == nil {
			return out, errors.New("pinned assessor backend unavailable")
		}
		if data, resultErr := readFile(assessor, "/tmp/assessment-output/result.json", 1<<20); resultErr == nil {
			var early RunOutput
			if strictCubeJSON(data, &early) == nil {
				out.Manifest = early.Manifest
			}
			return out, errors.New("assessment ended before application log checkpoint")
		}
		if err = waitCubePoll(ctx); err != nil {
			return out, err
		}
	}
	complete, err := health()
	if err != nil || !complete {
		return out, errors.New("target application log incomplete")
	}
	logs, err := readFile(target, "/tmp/target-api-access.jsonl", in.Profile.Limits.MaxLogBytes)
	if err != nil || len(logs) == 0 {
		return out, errors.New("bounded application log transfer incomplete")
	}
	logHash := SHA256(logs)
	if err = assessor.Files().Write(ctx, "/tmp/assessment-output/logs-input/application.jsonl", logs); err != nil {
		return out, errors.New("application log upload incomplete")
	}
	meta, _ := json.Marshal(map[string]any{"schema": "esf.supplied-application-logs/v1", "sha256": logHash, "bytes": len(logs), "complete": true})
	if err = assessor.Files().Write(ctx, "/tmp/assessment-output/logs-input/metadata.json", meta); err != nil {
		return out, errors.New("application log metadata upload incomplete")
	}
	var result RunOutput
	for {
		data, readErr := readFile(assessor, "/tmp/assessment-output/result.json", 1<<20)
		if readErr == nil {
			if err = strictCubeJSON(data, &result); err != nil {
				return out, errors.New("assessor result schema invalid")
			}
			break
		}
		if !errors.Is(readErr, os.ErrNotExist) {
			return out, errors.New("assessor result transfer incomplete")
		}
		if err = waitCubePoll(ctx); err != nil {
			return out, err
		}
	}
	out.Manifest = result.Manifest
	if err = out.Manifest.Validate(out.Expected); err != nil {
		return out, err
	}
	if result.SourceLogSHA256 != logHash {
		return out, errors.New("assessor application log binding mismatch")
	}
	out.PrivateEvidence = map[string][]byte{}
	out.ExpectedHashes = map[string]string{}
	var total int64
	for _, ref := range out.Manifest.Evidence {
		if path.Base(ref.Name) != ref.Name || !safeEvidenceName(ref.Name) || ref.Bytes <= 0 || ref.Bytes > in.Profile.Limits.MaxEvidenceBytes-total {
			return out, errors.New("selected evidence bounds invalid")
		}
		data, err := readFile(assessor, "/tmp/assessment-output/"+ref.Name, ref.Bytes)
		if err != nil || int64(len(data)) != ref.Bytes || SHA256(data) != ref.SHA256 {
			return out, errors.New("selected evidence transfer identity mismatch")
		}
		total += int64(len(data))
		out.PrivateEvidence[ref.Name] = data
		out.ExpectedHashes[ref.Name] = SHA256(data)
	}
	if result.ErrorCode != "" {
		return out, errors.New("assessment coverage incomplete")
	}
	return out, nil
}

func denyCubeRedirect(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }

func preserveCubeFailureContext(ctx context.Context, failure error) error {
	if failure == nil {
		return nil
	}
	return errors.Join(failure, ctx.Err())
}

func verifyCubeRuntimeTemplate(ctx context.Context, client *http.Client, cfg cube.Config, pin RuntimePin) error {
	if !identifierPattern.MatchString(pin.TemplateID) || !digestPattern.MatchString(strings.TrimPrefix(pin.ImageDigest, "sha256:")) || !strings.HasPrefix(pin.ImageDigest, "sha256:") {
		return errors.New("runtime template identity or image pin invalid")
	}
	headers := make(http.Header)
	if cfg.APIKey != "" {
		headers.Set("Authorization", "Bearer "+cfg.APIKey)
	}
	data, err := readBoundedHTTP(ctx, client, strings.TrimRight(cfg.APIURL, "/")+"/templates/"+url.PathEscape(pin.TemplateID), headers, 1<<20)
	if err != nil {
		return errors.New("bounded runtime template metadata unavailable")
	}
	var metadata struct {
		TemplateID    string `json:"templateID"`
		Status        string `json:"status"`
		ImageInfo     string `json:"imageInfo"`
		CreateRequest struct {
			Containers []struct {
				Image struct {
					Annotations map[string]string `json:"annotations"`
				} `json:"image"`
			} `json:"containers"`
		} `json:"createRequest"`
	}
	if json.Unmarshal(data, &metadata) != nil || metadata.TemplateID != pin.TemplateID || !strings.EqualFold(metadata.Status, "ready") {
		return errors.New("runtime template metadata identity unavailable")
	}
	if strings.HasSuffix(metadata.ImageInfo, "@"+pin.ImageDigest) {
		return nil
	}
	rootfs := pin.Assets["cube-rootfs-sha256"]
	if !digestPattern.MatchString(rootfs) || len(metadata.CreateRequest.Containers) != 1 || metadata.CreateRequest.Containers[0].Image.Annotations["cube.master.rootfs.artifact.sha256"] != rootfs {
		return errors.New("runtime template immutable artifact pin mismatch")
	}
	return nil
}

func strictCubeJSON(data []byte, value any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(value); err != nil {
		return err
	}
	if err := d.Decode(new(any)); !errors.Is(err, io.EOF) {
		return errors.New("trailing JSON")
	}
	return nil
}

func waitCubePoll(ctx context.Context) error {
	timer := time.NewTimer(200 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return fmt.Errorf("assessment interrupted: %w", ctx.Err())
	case <-timer.C:
		return nil
	}
}

func cubePrivateHeaders(sb *cubesandbox.Sandbox) http.Header {
	h := cubeTrafficHeaders(sb)
	h.Set("X-Access-Token", sb.EnvdAccessToken)
	return h
}

func cubeTrafficHeaders(sb *cubesandbox.Sandbox) http.Header {
	h := make(http.Header)
	h.Set("cube-traffic-access-token", sb.TrafficAccessToken)
	h.Set("e2b-traffic-access-token", sb.TrafficAccessToken)
	return h
}

func (e *CubeExecutor) dataClient() (*http.Client, error) {
	pool, err := x509.SystemCertPool()
	if err != nil {
		return nil, err
	}
	if e.config.PreviewCAFile != "" {
		data, err := os.ReadFile(e.config.PreviewCAFile)
		if err != nil || !pool.AppendCertsFromPEM(data) {
			return nil, errors.New("Cube preview CA unavailable")
		}
	}
	proxyAddress := net.JoinHostPort(e.config.Cube.ProxyNodeIP, strconv.Itoa(e.config.Cube.ProxyPortHTTP))
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}, Proxy: nil, ResponseHeaderTimeout: 10 * time.Second, DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, network, proxyAddress)
	}}
	return &http.Client{Transport: transport, Timeout: 30 * time.Second, CheckRedirect: denyCubeRedirect}, nil
}

func readBoundedHTTP(ctx context.Context, client *http.Client, address string, headers http.Header, max int64) ([]byte, error) {
	if max <= 0 || max > 32<<20 {
		return nil, errors.New("invalid file transfer bound")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	req.Header = headers.Clone()
	response, err := client.Do(req)
	if err != nil {
		return nil, errors.New("bounded data transport unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return nil, os.ErrNotExist
	}
	if response.StatusCode != http.StatusOK {
		return nil, errors.New("bounded data transport rejected response")
	}
	if response.ContentLength > max {
		return nil, errors.New("file transfer exceeds bound")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, max+1))
	if err != nil || int64(len(data)) > max {
		return nil, errors.New("file transfer incomplete or oversized")
	}
	return data, nil
}

// cleanupCubeOwned never reconnects or resumes a VM. A failed deletion does not
// prevent attempts for the remaining owner-labelled VMs. Unknown create acks
// stay incomplete and require operator reconciliation rather than an API retry.
func cleanupCubeOwned(ctx context.Context, client *cubesandbox.Client, httpClient *http.Client, cfg cube.Config, owner string, known []string, pending bool) (Cleanup, error) {
	result := Cleanup{Status: Incomplete, OwnedIDs: append([]string(nil), known...), PendingAllocation: pending}
	items, err := client.List(ctx)
	if err != nil {
		result.RemainingIDs = append([]string(nil), known...)
		return result, errors.New("owned Cube inventory unavailable")
	}
	owned := map[string]bool{}
	for _, item := range items {
		if item.Metadata["esf.security.owner"] == owner && identifierPattern.MatchString(item.SandboxID) {
			owned[item.SandboxID] = true
		}
	}
	for id := range owned {
		found := false
		for _, knownID := range result.OwnedIDs {
			found = found || id == knownID
		}
		if !found {
			result.OwnedIDs = append(result.OwnedIDs, id)
		}
	}
	for _, id := range result.OwnedIDs {
		if !owned[id] {
			continue
		}
		req, requestErr := http.NewRequestWithContext(ctx, http.MethodDelete, strings.TrimRight(cfg.APIURL, "/")+"/sandboxes/"+url.PathEscape(id), nil)
		if requestErr != nil {
			continue
		}
		if cfg.APIKey != "" {
			req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
		}
		response, deleteErr := httpClient.Do(req)
		if deleteErr == nil {
			io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
			response.Body.Close()
		}
	}
	items, err = client.List(ctx)
	if err != nil {
		result.RemainingIDs = append([]string(nil), result.OwnedIDs...)
		return result, errors.New("post-cleanup Cube inventory unavailable")
	}
	knownIDs := map[string]bool{}
	for _, id := range result.OwnedIDs {
		knownIDs[id] = true
	}
	for _, item := range items {
		if item.Metadata["esf.security.owner"] == owner || knownIDs[item.SandboxID] {
			result.RemainingIDs = append(result.RemainingIDs, item.SandboxID)
		}
	}
	sort.Strings(result.OwnedIDs)
	sort.Strings(result.RemainingIDs)
	if pending || len(result.RemainingIDs) > 0 {
		return result, errors.New("owned Cube cleanup incomplete")
	}
	result.Status = Complete
	return result, nil
}
