// turn-smoke performs an owned, secret-free Cube lifecycle qualification.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/mitkox/esf/internal/sandbox"
	"github.com/mitkox/esf/internal/sandbox/cube"
	"os"
	"time"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() (outErr error) {
	template := flag.String("template", "", "operator-approved Cube template")
	allowInternet := flag.Bool("allow-internet", false, "probe public-egress profile as well as metadata isolation")
	flag.Parse()
	if *template == "" {
		return errors.New("--template required")
	}
	cfg := cube.ConfigFromEnv()
	cfg.TemplateID = *template
	p, err := cube.New(cfg)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if err = p.Ping(ctx); err != nil {
		return err
	}
	nonce := make([]byte, 16)
	if _, err = rand.Read(nonce); err != nil {
		return err
	}
	owner := hex.EncodeToString(nonce)
	receipt := map[string]any{"owner": owner, "template": *template, "cleanup_verified": false}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 45*time.Second)
		defer stop()
		live, listErr := p.List(cleanup)
		if listErr == nil {
			for _, vm := range live {
				if vm.Metadata["turn-smoke-owner"] == owner {
					if e := p.Destroy(cleanup, vm.ID); e != nil {
						outErr = errors.Join(outErr, e)
					}
				}
			}
		}
		after, verifyErr := p.List(cleanup)
		remaining := []string{}
		for _, vm := range after {
			if vm.Metadata["turn-smoke-owner"] == owner {
				remaining = append(remaining, vm.ID)
			}
		}
		verified := listErr == nil && verifyErr == nil && len(remaining) == 0
		receipt["cleanup_verified"] = verified
		receipt["residual"] = remaining
		if !verified {
			outErr = errors.Join(outErr, errors.New("owned sandbox cleanup unverified"), listErr, verifyErr)
		}
		_ = json.NewEncoder(os.Stdout).Encode(receipt)
	}()
	vm, err := p.Create(ctx, sandbox.Spec{Template: *template, Metadata: map[string]string{"turn-smoke-owner": owner}, IdleTimeout: 2 * time.Minute, Network: sandbox.Network{AllowInternet: allowInternet}})
	if err != nil {
		return err
	}
	receipt["sandbox_id"] = vm.ID()
	result, err := vm.Execute(ctx, sandbox.Command{Argv: []string{"python3", "-c", "print('ESF_AZURE_CUBE_OK')"}, Timeout: 20 * time.Second})
	if err != nil {
		return err
	}
	receipt["exit_code"] = result.ExitCode
	receipt["stdout"] = result.Stdout
	if !result.Succeeded() {
		return errors.New("Cube probe command failed")
	}
	payload := []byte{0, 255, 128, 42}
	if err = vm.WriteFile(ctx, "/tmp/esf-smoke.bin", payload); err != nil {
		return err
	}
	actual, err := vm.ReadFile(ctx, "/tmp/esf-smoke.bin")
	if err != nil {
		return err
	}
	if string(actual) != string(payload) {
		return errors.New("binary file round trip failed")
	}
	receipt["binary_roundtrip"] = true
	// Never request an identity token or print a response body. Any metadata
	// HTTP response means guest isolation has not been established.
	probe := `import urllib.request,urllib.error,json,sys
try:
 r=urllib.request.urlopen(urllib.request.Request("http://169.254.169.254/metadata/instance?api-version=2021-02-01",headers={"Metadata":"true"}),timeout=5)
 print(json.dumps({"metadata_http_status":r.status}));sys.exit(2)
except urllib.error.HTTPError as e:
 print(json.dumps({"metadata_http_status":e.code}));sys.exit(2)
except (urllib.error.URLError,TimeoutError,OSError):
 print(json.dumps({"metadata_http_status":None}))
`
	metadata, err := vm.Execute(ctx, sandbox.Command{Argv: []string{"python3", "-c", probe}, Timeout: 10 * time.Second})
	if err != nil {
		return err
	}
	receipt["metadata_probe"] = metadata.Stdout
	receipt["allow_internet"] = *allowInternet
	if !metadata.Succeeded() {
		return errors.New("guest metadata endpoint is reachable")
	}
	receipt["metadata_isolated"] = true
	return nil
}
