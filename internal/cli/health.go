package cli

import (
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/mitkox/esf/internal/config"
	"github.com/spf13/cobra"
)

func newHealthCommand(options *commandOptions) *cobra.Command {
	return &cobra.Command{Use: "health", Short: "Check the loopback control plane and database", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		cfg, err := config.LoadConfig(options.configPath)
		if err != nil {
			return err
		}
		host, _, err := net.SplitHostPort(cfg.Server.Listen)
		if err != nil || (!strings.EqualFold(host, "localhost") && (net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback())) {
			return fmt.Errorf("health target must use a loopback address")
		}
		client := &http.Client{Timeout: 2 * time.Second}
		request, err := http.NewRequestWithContext(cmd.Context(), http.MethodGet, "http://"+cfg.Server.Listen+"/healthz", nil)
		if err != nil {
			return err
		}
		response, err := client.Do(request)
		if err != nil {
			return fmt.Errorf("control plane unavailable: %w", err)
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusNoContent {
			return fmt.Errorf("control plane health returned HTTP %d", response.StatusCode)
		}
		return nil
	}}
}
