package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mitkox/esf/internal/evidence"
	"github.com/mitkox/esf/internal/secretfile"
)

// maxBundleBytes bounds a bundle read. Evidence is small: patch digests, gate
// digests and identity, never the patch or the logs themselves.
const maxBundleBytes = 16 << 20

func newVerifyCommand() *cobra.Command {
	var bundlePath, keyPath string
	command := &cobra.Command{
		Use:          "verify",
		Short:        "Verify a change-evidence bundle offline",
		SilenceUsage: true,
		Long: `Verify a signed DSSE change-evidence bundle with only a public key.

Verification performs no network access and needs no certificate authority or
transparency log. It checks the envelope signature, then the statement and
predicate types, and prints the run, subjects and gate summary the bundle
carries.

The public key is read through the private-file rules: a regular file with
owner-only permissions (chmod 600).`,
		Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if strings.TrimSpace(bundlePath) == "" {
				return errors.New("--bundle is required")
			}
			if strings.TrimSpace(keyPath) == "" {
				return errors.New("--key is required")
			}
			body, err := readBundleFile(bundlePath)
			if err != nil {
				return err
			}
			var envelope evidence.Envelope
			if err := json.Unmarshal(body, &envelope); err != nil {
				return fmt.Errorf("decode bundle %q: %w", bundlePath, err)
			}
			keyBody, err := secretfile.Read(keyPath, 16<<10)
			if err != nil {
				return fmt.Errorf("read public key: %w", err)
			}
			publicKey, err := evidence.ParsePublicKey(keyBody)
			if err != nil {
				return err
			}
			statement, err := evidence.Verify(&envelope, publicKey)
			if err != nil {
				return err
			}
			printVerification(command, bundlePath, statement)
			return nil
		},
	}
	command.Flags().StringVar(&bundlePath, "bundle", "", "path to the DSSE evidence bundle")
	command.Flags().StringVar(&keyPath, "key", "", "private 0600 file holding the ed25519 public key in PEM format")
	return command
}

func readBundleFile(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("read bundle: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("read bundle: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("bundle must be a regular file")
	}
	if info.Size() > maxBundleBytes {
		return nil, fmt.Errorf("bundle exceeds %d bytes", maxBundleBytes)
	}
	body, err := io.ReadAll(io.LimitReader(file, maxBundleBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read bundle: %w", err)
	}
	if len(body) > maxBundleBytes {
		return nil, fmt.Errorf("bundle exceeds %d bytes", maxBundleBytes)
	}
	return body, nil
}

func printVerification(command *cobra.Command, bundlePath string, statement *evidence.Statement) {
	out := command.OutOrStdout()
	fmt.Fprintf(out, "Bundle:    %s\n", bundlePath)
	fmt.Fprintf(out, "Run:       %s\n", statement.Predicate.Run.RunID)
	fmt.Fprintf(out, "Predicate: %s\n", statement.PredicateType)
	subjects := make([]string, 0, len(statement.Subject))
	for _, subject := range statement.Subject {
		algorithms := make([]string, 0, len(subject.Digest))
		for algorithm := range subject.Digest {
			algorithms = append(algorithms, algorithm)
		}
		sort.Strings(algorithms)
		for _, algorithm := range algorithms {
			subjects = append(subjects, fmt.Sprintf("%s %s:%s", subject.Name, algorithm, subject.Digest[algorithm]))
		}
	}
	fmt.Fprintf(out, "Subjects:  %s\n", strings.Join(subjects, ", "))
	passed, failed := 0, 0
	for _, gate := range statement.Predicate.Verification.Gates {
		if gate.Passed {
			passed++
		} else {
			failed++
		}
	}
	fmt.Fprintf(out, "Gates:     %d passed, %d failed\n", passed, failed)
	fmt.Fprintln(out, "Result:    VERIFIED with the supplied ed25519 public key")
}
