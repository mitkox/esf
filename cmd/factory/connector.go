package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"

	"github.com/mitkox/esf/internal/connector"
	"github.com/mitkox/esf/internal/factory"
	artifacts "github.com/mitkox/esf/internal/factoryartifacts"
	"github.com/mitkox/esf/internal/forge"
	"github.com/mitkox/esf/internal/forge/azuredevops"
	"github.com/mitkox/esf/internal/httpsecurity"
)

// newConnectorCommand runs the host-side connector workload.
//
// It is deliberately separate from the Temporal worker: the worker executes
// runs, while this process carries the inbound forge credentials, serves the
// authenticated webhook receivers, and reconciles each connector on a schedule.
func newConnectorCommand(configPath *string) *cobra.Command {
	return &cobra.Command{
		Use:   "connector",
		Short: "Run forge connector intake (reconciliation and webhooks)",
		Long: `Run every configured forge connector's intake workload.

Each connector reconciles its work-item revisions on the configured interval and
serves an authenticated webhook receiver. The receiver binds loopback by
default; a non-loopback address requires native TLS. Forge credentials are read
from the same private token files the worker uses and never enter a Cube
sandbox.

  [connectors.azuredevops]
  kind        = "azuredevops"
  base_url    = "https://dev.azure.com/acme"
  token_file  = "/etc/factory/azuredevops.token"
  webhook_listen = "127.0.0.1:8091"
  sweep_interval = "1m"`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(command *cobra.Command, _ []string) error {
			ctx := command.Context()
			cfg, err := loadConfig(*configPath)
			if err != nil {
				return err
			}
			if err := cfg.Validate(); err != nil {
				return err
			}
			if len(cfg.Connectors) == 0 {
				return errors.New("no connectors are enabled; configure at least one [connectors.<name>] table")
			}
			logger := slog.New(slog.NewTextHandler(command.ErrOrStderr(), &slog.HandlerOptions{Level: slog.LevelInfo}))
			temporalClient, err := factory.NewTemporalClient(cfg, logger)
			if err != nil {
				return err
			}
			defer temporalClient.Close()
			sink := &connectorIntake{
				client:    temporalClient,
				taskQueue: cfg.Temporal.TaskQueue,
				dataDir:   cfg.Storage.DataDir,
				logger:    logger,
			}
			artifactLimit := cfg.Limits.ArtifactBytes
			if artifactLimit <= 0 {
				artifactLimit = artifacts.DefaultMaxArtifactBytes
			}
			artifactFactory, err := artifacts.NewLocalWithLimit(cfg.Storage.DataDir, artifactLimit)
			if err != nil {
				return fmt.Errorf("connector health: %w", err)
			}
			telemetry, err := factory.NewTelemetry(ctx, cfg.Observability)
			if err != nil {
				return fmt.Errorf("connector telemetry: %w", err)
			}
			defer func() { _ = telemetry.Shutdown(context.Background()) }()
			healthReporter, err := factory.NewConnectorHealthReporter(artifactFactory, telemetry, logger)
			if err != nil {
				return err
			}
			specs, err := buildConnectorRuntime(cfg, sink, healthReporter)
			if err != nil {
				return err
			}
			runtime, err := connector.New(specs, connector.Options{
				Logger: logger,
				OnListen: func(name string, address net.Addr, tlsEnabled bool) {
					scheme := "http"
					if tlsEnabled {
						scheme = "https"
					}
					fmt.Fprintf(command.OutOrStdout(), "factory connector: %s webhook listening on %s://%s\n", name, scheme, address)
				},
			})
			if err != nil {
				return err
			}
			return runtime.Run(ctx)
		},
	}
}

// buildConnectorRuntime constructs one intake loop per configured connector.
//
// It reads every credential through Config.Connector and builds the adapter
// through the same path the worker uses, so a connector cannot be configured
// differently in the two workloads.
func buildConnectorRuntime(cfg factory.Config, sink forge.IntakeSink, healthReporter *factory.ConnectorHealthReporter) ([]connector.Spec, error) {
	names := make([]string, 0, len(cfg.Connectors))
	for name := range cfg.Connectors {
		names = append(names, name)
	}
	sort.Strings(names)
	specs := make([]connector.Spec, 0, len(names))
	for _, name := range names {
		connectorConfig := cfg.Connectors[name]
		settings, err := cfg.NewForgeConnector(name)
		if err != nil {
			return nil, err
		}
		azure, ok := settings.(*azuredevops.Client)
		if !ok {
			return nil, fmt.Errorf("connector %q: the connector workload does not support kind %q", name, connectorConfig.Kind)
		}
		tlsConfig, err := httpsecurity.LoadTLSConfig(connectorConfig.WebhookTLSCertFile, connectorConfig.WebhookTLSKeyFile)
		if err != nil {
			return nil, fmt.Errorf("connector %q: webhook TLS: %w", name, err)
		}
		listen := connectorConfig.WebhookListenAddress()
		if err := httpsecurity.ValidateListen(listen, tlsConfig != nil); err != nil {
			return nil, fmt.Errorf("connector %q: webhook listener: %w", name, err)
		}
		watermark, err := connectorWatermark(cfg.Storage.DataDir, name)
		if err != nil {
			return nil, err
		}
		sweeper, err := azuredevops.NewSweeper(azure, watermark, azuredevops.SweeperOptions{})
		if err != nil {
			return nil, fmt.Errorf("connector %q: %w", name, err)
		}
		webhook, err := azuredevops.NewWebhook(azure, azuredevops.WebhookConfig{
			Connector: name,
			Sink:      sink,
			Repositories: forge.RepositoryResolverFunc(func(fields forge.WorkItemFields) (string, error) {
				resolved, err := cfg.ResolvePipelineForConnector(name, fields)
				if err != nil {
					return "", err
				}
				return resolved.Repository, nil
			}),
		})
		if err != nil {
			return nil, fmt.Errorf("connector %q: %w", name, err)
		}
		specs = append(specs, connector.Spec{
			Name:           name,
			SweepInterval:  connectorConfig.SweepIntervalOrDefault(),
			Sweeper:        sweeper,
			Deliver:        sweepDeliverer(cfg, name, azure, sink),
			WebhookListen:  listen,
			WebhookHandler: webhook,
			TLSConfig:      tlsConfig,
			Observe:        connectorHealthObserver(healthReporter),
			Subscription: func(ctx context.Context) (forge.SubscriptionState, string) {
				state, detail, err := azure.SubscriptionState(ctx)
				if err != nil {
					return forge.SubscriptionUnknown, "subscription state unavailable"
				}
				return state, detail
			},
		})
	}
	return specs, nil
}

// sweepDeliverer re-resolves the operator mapping and repository permission for
// a reconciled work item before proposing it.
//
// A sweep has no delivery payload to authorise, so the actor resolved from the
// authoritative work-item response is checked against the mapped repository
// exactly as a webhook delivery is.
func sweepDeliverer(cfg factory.Config, name string, azure *azuredevops.Client, sink forge.IntakeSink) connector.Deliverer {
	return func(ctx context.Context, candidate forge.Candidate) error {
		rejection := forge.Rejection{
			Connector:  name,
			WorkItemID: candidate.WorkItem.ID,
			Revision:   candidate.WorkItem.Revision,
			Actor:      candidate.Actor,
		}
		if strings.TrimSpace(candidate.Actor.Descriptor) == "" {
			rejection.Reason = "actor identity unavailable"
			return sink.Reject(ctx, rejection)
		}
		resolved, err := cfg.ResolvePipelineForConnector(name, candidate.WorkItem.MappingFields())
		if err != nil {
			rejection.Reason = err.Error()
			return sink.Reject(ctx, rejection)
		}
		allowed, err := azure.RepositoryWrite(ctx, resolved.Repository, candidate.Actor.Descriptor)
		if err != nil {
			return err
		}
		if !allowed {
			rejection.Reason = "actor lacks repository write permission"
			return sink.Reject(ctx, rejection)
		}
		return sink.Propose(ctx, forge.Proposal{
			Candidate:            candidate,
			DeliveryID:           "sweep:" + candidate.WorkItem.Revision,
			RepositoryPermission: forge.PermissionWrite,
		})
	}
}

// connectorHealthObserver adapts the connector runtime's liveness observations
// to the factory health reporter that feeds the console snapshot and metrics.
func connectorHealthObserver(reporter *factory.ConnectorHealthReporter) connector.HealthObserver {
	if reporter == nil {
		return nil
	}
	return func(ctx context.Context, observation connector.HealthObservation) error {
		_, err := reporter.Report(ctx, factory.ConnectorHealthInput{
			Connector:           observation.Connector,
			LastSuccessfulSweep: observation.LastSuccessfulSweep,
			LastDelivery:        observation.LastDelivery,
			Subscription:        string(observation.Subscription),
			SubscriptionDetail:  observation.SubscriptionDetail,
		})
		return err
	}
}

// connectorWatermark derives the durable reconciliation watermark path from
// the connector name. Hashing keeps an operator-chosen name from escaping the
// data directory.
func connectorWatermark(dataDir, connectorName string) (*azuredevops.FileWatermark, error) {
	digest := sha256.Sum256([]byte(connectorName))
	path := filepath.Join(dataDir, "connector-watermarks", hex.EncodeToString(digest[:16])+".watermark")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("connector %q: create watermark directory: %w", connectorName, err)
	}
	watermark, err := azuredevops.NewFileWatermark(path)
	if err != nil {
		return nil, fmt.Errorf("connector %q: %w", connectorName, err)
	}
	return watermark, nil
}

// connectorIntake starts one deterministic work-item workflow per delivery.
//
// The workflow ID derives from connector, work item, and revision, so a
// repeated delivery converges on the same workflow instead of starting a
// second run.
type connectorIntake struct {
	client    client.Client
	taskQueue string
	dataDir   string
	logger    *slog.Logger
}

func (i *connectorIntake) Propose(ctx context.Context, proposal forge.Proposal) error {
	connectorName := strings.TrimSpace(proposal.Connector)
	workItemID := strings.TrimSpace(proposal.WorkItem.ID)
	revision := strings.TrimSpace(proposal.WorkItem.Revision)
	if connectorName == "" || workItemID == "" || revision == "" {
		return fmt.Errorf("connector intake: connector, work item id, and revision are required")
	}
	workflowID := factory.WorkItemWorkflowID(connectorName, workItemID, revision)
	_, err := i.client.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:        workflowID,
		TaskQueue: i.taskQueue,
		// A duplicate delivery reuses a running workflow; a closed workflow is
		// already recorded and is not restarted.
		WorkflowIDConflictPolicy: enumspb.WORKFLOW_ID_CONFLICT_POLICY_USE_EXISTING,
		WorkflowIDReusePolicy:    enumspb.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
	}, factory.WorkItemWorkflow, factory.WorkItemWorkflowInput{Proposal: proposal})
	if err != nil {
		var started *serviceerror.WorkflowExecutionAlreadyStarted
		if errors.As(err, &started) {
			i.logger.InfoContext(ctx, "connector intake duplicate",
				"connector", connectorName, "work_item_id", workItemID, "revision", revision)
			return nil
		}
		return fmt.Errorf("start work-item workflow %s: %w", workflowID, err)
	}
	i.logger.InfoContext(ctx, "connector intake accepted",
		"connector", connectorName, "work_item_id", workItemID, "revision", revision, "workflow_id", workflowID)
	return nil
}

func (i *connectorIntake) Reject(ctx context.Context, rejection forge.Rejection) error {
	i.logger.WarnContext(ctx, "connector intake rejected",
		"connector", rejection.Connector,
		"work_item_id", rejection.WorkItemID,
		"revision", rejection.Revision,
		"reason", rejection.Reason)
	if err := factory.AppendIntakeRejection(i.dataDir, rejection); err != nil {
		return err
	}
	return nil
}
