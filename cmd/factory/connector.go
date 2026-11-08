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
			healthReporter, err := factory.NewConnectorHealthReporter(cfg, artifactFactory, telemetry, logger)
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
func buildConnectorRuntime(cfg factory.Config, sink forge.IntakeSink, reporters ...*factory.ConnectorHealthReporter) ([]connector.Spec, error) {
	var healthReporter *factory.ConnectorHealthReporter
	if len(reporters) > 0 {
		healthReporter = reporters[0]
	}
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

// connectorIntake starts one deterministic work-item workflow per work item.
//
// The workflow ID derives from connector and work item, so a newer revision is
// signalled to the running execution instead of starting a second run. A closed
// execution under that ID is not a duplicate delivery: a later revision starts
// a fresh run, and the durable intake claim rejects true duplicates before any
// child starts.
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
	if running, err := i.runningWorkItemWorkflow(ctx, connectorName, workItemID, revision); err != nil {
		return err
	} else if running != "" {
		return i.signalRevision(ctx, running, connectorName, workItemID, revision, proposal)
	}

	workflowID := factory.WorkItemWorkflowID(connectorName, workItemID)
	_, err := i.client.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:        workflowID,
		TaskQueue: i.taskQueue,
		// A running execution wins the race; a closed one may be followed by a
		// later revision under the same stable ID. Duplicate delivery
		// protection lives in the durable intake claim, not in the ID.
		WorkflowIDConflictPolicy: enumspb.WORKFLOW_ID_CONFLICT_POLICY_FAIL,
		WorkflowIDReusePolicy:    enumspb.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE,
	}, factory.WorkItemWorkflow, factory.WorkItemWorkflowInput{Proposal: proposal})
	if err != nil {
		var started *serviceerror.WorkflowExecutionAlreadyStarted
		if errors.As(err, &started) {
			// Another delivery started the execution after the lookup; deliver
			// this revision to it instead of failing the intake.
			return i.signalRevision(ctx, workflowID, connectorName, workItemID, revision, proposal)
		}
		return fmt.Errorf("start work-item workflow %s: %w", workflowID, err)
	}
	i.logger.InfoContext(ctx, "connector intake accepted",
		"connector", connectorName, "work_item_id", workItemID, "revision", revision, "workflow_id", workflowID)
	return nil
}

// runningWorkItemWorkflow returns the ID of a running execution for the work
// item, or an empty string when none runs.
//
// The revision-keyed legacy ID is checked for the same delivery so a workflow
// started before the identity became stable receives the signal rather than
// being duplicated by a second execution.
func (i *connectorIntake) runningWorkItemWorkflow(ctx context.Context, connectorName, workItemID, revision string) (string, error) {
	for _, workflowID := range []string{
		factory.WorkItemWorkflowID(connectorName, workItemID),
		factory.LegacyWorkItemWorkflowID(connectorName, workItemID, revision),
	} {
		running, err := i.workflowRunning(ctx, workflowID)
		if err != nil {
			return "", err
		}
		if running {
			return workflowID, nil
		}
	}
	return "", nil
}

// workflowRunning reports whether an execution exists and is still running. A
// closed execution is not an error: it is a later revision of the same work
// item, which starts a fresh run.
func (i *connectorIntake) workflowRunning(ctx context.Context, workflowID string) (bool, error) {
	description, err := i.client.DescribeWorkflowExecution(ctx, workflowID, "")
	if err != nil {
		var notFound *serviceerror.NotFound
		if errors.As(err, &notFound) {
			return false, nil
		}
		return false, fmt.Errorf("describe work-item workflow %s: %w", workflowID, err)
	}
	info := description.GetWorkflowExecutionInfo()
	return info != nil && info.GetStatus() == enumspb.WORKFLOW_EXECUTION_STATUS_RUNNING, nil
}

func (i *connectorIntake) signalRevision(ctx context.Context, workflowID, connectorName, workItemID, revision string, proposal forge.Proposal) error {
	if err := i.client.SignalWorkflow(ctx, workflowID, "", factory.WorkItemRevisionSignalName,
		factory.WorkItemRevisionSignal{Proposal: proposal}); err != nil {
		return fmt.Errorf("signal work-item workflow %s: %w", workflowID, err)
	}
	i.logger.InfoContext(ctx, "connector intake revision signalled",
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
