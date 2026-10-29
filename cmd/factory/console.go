package main

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/mitkox/esf/internal/console"
	"github.com/mitkox/esf/internal/factory"
)

// newConsoleCommand serves the read-only factory console.
//
// The console is an operator surface over durable evidence, not a second
// control plane: it opens no Temporal namespace, mutates no run and creates no
// sandbox. Actions that change factory state stay in the authenticated CLI.
func newConsoleCommand(configPath *string) *cobra.Command {
	var (
		listen      string
		tokenFile   string
		noAuth      bool
		live        bool
		environment string
		dataDir     string
		refresh     string
		tlsCertFile string
		tlsKeyFile  string
	)
	command := &cobra.Command{
		Use:   "console",
		Short: "Serve the read-only factory admin console",
		Long: `Serve the factory admin console on a loopback address.

The console reads the durable artifact store (runs/, changes/) and the operator
configuration. It never mutates factory state: review decisions and cancellation
remain in the factory CLI.

Authentication uses a bearer read token. Generate one and keep it in a private
file:

  openssl rand -hex 32 > ~/.config/esf/console.token
  chmod 600 ~/.config/esf/console.token
  factory console --read-token-file ~/.config/esf/console.token

For a throwaway local evaluation you may pass --no-auth together with a
loopback listen address. That combination is refused on any other address.

Examples:

  factory console --listen 127.0.0.1:7331 --read-token-file ~/.config/esf/console.token
  factory console --listen 127.0.0.1:8443 --read-token-file ~/.config/esf/console.token \
    --tls-cert-file ~/.config/esf/tls/console.crt --tls-key-file ~/.config/esf/tls/console.key
  factory console --no-auth --listen 127.0.0.1:7331 --environment development`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE: func(command *cobra.Command, _ []string) error {
			cfg, err := loadConfig(*configPath)
			if err != nil {
				return err
			}
			token := ""
			if strings.TrimSpace(tokenFile) != "" {
				token, err = readConsoleTokenFile(tokenFile)
				if err != nil {
					return err
				}
			}
			if noAuth && token != "" {
				return fmt.Errorf("--no-auth and --read-token-file are mutually exclusive")
			}
			if noAuth && !isLoopbackHost(listen) {
				return fmt.Errorf("--no-auth is only allowed with a loopback --listen address")
			}
			resolvedDataDir := strings.TrimSpace(dataDir)
			if resolvedDataDir == "" {
				resolvedDataDir = cfg.Storage.DataDir
			}
			if resolvedDataDir == "" {
				resolvedDataDir = ".factory"
			}
			absDataDir, err := filepath.Abs(resolvedDataDir)
			if err != nil {
				return fmt.Errorf("resolve data directory: %w", err)
			}
			if strings.TrimSpace(environment) == "" {
				environment = strings.TrimSpace(os.Getenv("FACTORY_ENVIRONMENT"))
			}
			refreshInterval, err := parseConsoleRefresh(refresh)
			if err != nil {
				return err
			}
			tlsCertFile, tlsKeyFile = resolveConsoleTLSFlags(tlsCertFile, tlsKeyFile)
			scheme := "http"
			if strings.TrimSpace(tlsCertFile) != "" || strings.TrimSpace(tlsKeyFile) != "" {
				scheme = "https"
			}

			server, err := console.New(console.Config{
				Factory:         cfg,
				ConfigPath:      *configPath,
				DataDir:         absDataDir,
				ReadToken:       token,
				AllowNoAuth:     noAuth,
				Environment:     environment,
				Listen:          listen,
				Live:            live,
				Version:         factory.Version,
				RefreshInterval: refreshInterval,
				TLSCertFile:     tlsCertFile,
				TLSKeyFile:      tlsKeyFile,
			})
			if err != nil {
				return err
			}
			defer server.Close()
			return server.Serve(command.Context(), listen, func(address net.Addr) {
				fmt.Fprintf(command.OutOrStdout(), "factory console: serving %s://%s\n", scheme, address.String())
				fmt.Fprintf(command.OutOrStdout(), "factory console: evidence root %s\n", absDataDir)
				if noAuth {
					fmt.Fprintln(command.OutOrStdout(), "factory console: WARNING no authentication is enabled (loopback evaluation only)")
				} else {
					fmt.Fprintln(command.OutOrStdout(), "factory console: bearer token required; the token is not printed")
				}
			})
		},
	}
	command.Flags().StringVar(&listen, "listen", "127.0.0.1:7331", "loopback listen address")
	command.Flags().StringVar(&tokenFile, "read-token-file", "", "private file holding the console bearer token")
	command.Flags().BoolVar(&noAuth, "no-auth", false, "disable authentication (loopback listeners only; local evaluation)")
	command.Flags().BoolVar(&live, "live", true, "query Temporal and CubeSandbox for live, read-only state")
	command.Flags().StringVar(&environment, "environment", "", "environment name shown in the console (default: $FACTORY_ENVIRONMENT or self-hosted)")
	command.Flags().StringVar(&dataDir, "data-dir", "", "artifact store root (default: factory storage.data_dir)")
	command.Flags().StringVar(&refresh, "refresh", "", "evidence refresh interval, for example 15s (default 15s)")
	command.Flags().StringVar(&tlsCertFile, "tls-cert-file", "", "PEM certificate (with --tls-key-file) to serve HTTPS (env FACTORY_CONSOLE_TLS_CERT_FILE)")
	command.Flags().StringVar(&tlsKeyFile, "tls-key-file", "", "PEM private key (0600) paired with --tls-cert-file (env FACTORY_CONSOLE_TLS_KEY_FILE)")
	return command
}

// readConsoleTokenFile validates and loads a bearer token file. The rules match
// the control plane's: a small, private, regular file, never a symlink.
func readConsoleTokenFile(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", fmt.Errorf("read token file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("read token file must be a regular file, not a symlink or device")
	}
	if info.Size() > 4096 {
		return "", fmt.Errorf("read token file must be at most 4096 bytes")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("read token file permissions %04o expose it to group or other users; chmod 600 it", info.Mode().Perm())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read token file: %w", err)
	}
	token := strings.TrimSpace(string(data))
	if len(token) < 32 || strings.ContainsAny(token, " \t\r\n") {
		return "", fmt.Errorf("read token must be at least 32 non-whitespace characters")
	}
	return token, nil
}

func isLoopbackHost(listen string) bool {
	host, _, err := net.SplitHostPort(listen)
	if err != nil {
		host = listen
	}
	host = strings.Trim(strings.TrimSpace(host), "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func parseConsoleRefresh(raw string) (time.Duration, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	duration, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("--refresh must be a Go duration such as 15s or 1m: %w", err)
	}
	if duration < time.Second {
		return 0, fmt.Errorf("--refresh must be at least 1s")
	}
	return duration, nil
}

// resolveConsoleTLSFlags applies the environment defaults for native TLS.
// Explicit flags win so a service manager can override the environment
// without rewriting the unit; both values are trimmed because systemd
// EnvironmentFile entries often keep trailing whitespace.
func resolveConsoleTLSFlags(certFlag, keyFlag string) (string, string) {
	cert := strings.TrimSpace(certFlag)
	if cert == "" {
		cert = strings.TrimSpace(os.Getenv("FACTORY_CONSOLE_TLS_CERT_FILE"))
	}
	key := strings.TrimSpace(keyFlag)
	if key == "" {
		key = strings.TrimSpace(os.Getenv("FACTORY_CONSOLE_TLS_KEY_FILE"))
	}
	return cert, key
}
