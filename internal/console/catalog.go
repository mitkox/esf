package console

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/mitkox/esf/internal/factory"
)

// buildCubeInfo returns the non-secret Cube configuration. The API key is
// deliberately reduced to a presence boolean.
func (s *Server) buildCubeInfo() CubeInfo {
	cube := s.cfg.Factory.Cube
	proxy := ""
	if cube.ProxyNodeIP != "" {
		proxy = cube.ProxyNodeIP
		if cube.ProxyPortHTTP > 0 {
			proxy = fmt.Sprintf("%s:%d", cube.ProxyNodeIP, cube.ProxyPortHTTP)
		}
	}
	return CubeInfo{
		APIURL:             cube.APIURL,
		TemplateID:         cube.TemplateID,
		Proxy:              proxy,
		Version:            cube.Version,
		CredentialsPresent: strings.TrimSpace(cube.APIKey) != "",
	}
}

func (s *Server) buildTemporalInfo() TemporalInfo {
	temporal := s.cfg.Factory.Temporal
	return TemporalInfo{
		HostPort:       temporal.HostPort,
		Namespace:      temporal.Namespace,
		TaskQueue:      temporal.TaskQueue,
		TLS:            temporal.TLS,
		APIKeyEnv:      temporal.APIKeyEnv,
		PayloadKeyring: strings.TrimSpace(temporal.PayloadKeyring) != "",
	}
}

func (s *Server) buildObservabilityInfo() ObservabilityInfo {
	observability := s.cfg.Factory.Observability
	return ObservabilityInfo{
		OTLPConfigured: strings.TrimSpace(observability.OTLPEndpoint) != "",
		ServiceName:    observability.ServiceName,
	}
}

func (s *Server) buildStorageInfo(runCount, changeCount int) StorageInfo {
	return StorageInfo{
		DataDir:     s.root,
		RunCount:    runCount,
		ChangeCount: changeCount,
	}
}

func (s *Server) buildLimitsCatalog() LimitsCatalog {
	limits := s.cfg.Factory.Limits
	return LimitsCatalog{
		CommandOutputBytes:    limits.CommandOutputBytes,
		ArtifactBytes:         limits.ArtifactBytes,
		WorkerConcurrency:     limits.WorkerConcurrency,
		AgentTimeoutMS:        millis(limits.AgentTimeout.Std()),
		VerificationTimeoutMS: millis(limits.VerificationTimeout.Std()),
		TotalTimeoutMS:        millis(limits.TotalTimeout.Std()),
		SandboxIdleTimeoutMS:  millis(limits.SandboxIdleTimeout.Std()),
		CPUMilli:              limits.CPUMilli,
		MemoryMiB:             limits.MemoryMiB,
		DiskMiB:               limits.DiskMiB,
	}
}

func (s *Server) buildSandboxCatalog() SandboxCatalog {
	sandbox := s.cfg.Factory.Sandbox
	return SandboxCatalog{
		AllowAttach:     sandbox.AllowAttach,
		AllowPreview:    sandbox.AllowPreview,
		BasePackages:    append([]string(nil), sandbox.BasePackages...),
		SetupScript:     strings.TrimSpace(sandbox.SetupScript) != "",
		RuntimeAllowOut: append([]string(nil), sandbox.RuntimeAllowOut...),
	}
}

func (s *Server) buildReviewCatalog() ReviewCatalog {
	review := s.cfg.Factory.Review
	return ReviewCatalog{
		Enabled:      review.Enabled,
		TimeoutMS:    millis(review.Timeout.Std()),
		Suspend:      review.Suspend,
		PreviewPorts: append([]int(nil), review.PreviewPorts...),
	}
}

func (s *Server) buildHardeningCatalog() HardeningCatalog {
	hardening := s.cfg.Factory.Hardening
	return HardeningCatalog{
		AcknowledgeOpenEgress:     hardening.AcknowledgeOpenEgress,
		EgressProbeEnabled:        hardening.EgressProbeEnabled(),
		EgressCanaryURL:           hardening.CanaryURL(),
		MetadataProbeURL:          hardening.MetadataURL(),
		BehaviorMonitorEnabled:    hardening.BehaviorMonitorEnabled(),
		TripSeverity:              hardening.TripLevel(),
		RequireNonRoot:            hardening.RequireNonRoot,
		AllowGateSelfModification: hardening.AllowGateSelfModification,
		AlertWebhookConfigured:    hardening.AlertsEnabled(),
		AlertTimeoutMS:            millis(hardening.Timeout()),
	}
}

func (s *Server) buildSecurityPosture() SecurityPosture {
	hardening := s.cfg.Factory.Hardening
	return SecurityPosture{
		EgressProbeEnabled:        hardening.EgressProbeEnabled(),
		BehaviorMonitorEnabled:    hardening.BehaviorMonitorEnabled(),
		TripSeverity:              hardening.TripLevel(),
		RequireNonRoot:            hardening.RequireNonRoot,
		AcknowledgeOpenEgress:     hardening.AcknowledgeOpenEgress,
		AllowGateSelfModification: hardening.AllowGateSelfModification,
		AlertWebhookConfigured:    hardening.AlertsEnabled(),
		EgressCanaryURL:           hardening.CanaryURL(),
	}
}

func (s *Server) buildIntakeCatalog() IntakeCatalog {
	intake := s.cfg.Factory.Intake
	return IntakeCatalog{Enabled: intake.Enabled, Model: intake.Model, TimeoutMS: millis(intake.Timeout.Std())}
}

func (s *Server) buildHarnessCatalog() []HarnessCatalog {
	harnesses := make([]HarnessCatalog, 0, len(s.cfg.Factory.Harnesses))
	for name, harness := range s.cfg.Factory.Harnesses {
		harnesses = append(harnesses, HarnessCatalog{
			Name:           name,
			Type:           harness.Type,
			Model:          harness.Model,
			Provider:       harness.Provider,
			ThinkingLevel:  harness.ThinkingLevel,
			TimeoutSeconds: int64(harness.Timeout.Std().Seconds()),
			Preinstalled:   harness.Preinstalled,
			Binary:         harness.Binary,
			BinaryPinned:   strings.TrimSpace(harness.BinarySHA256) != "",
			RuntimePinned:  strings.TrimSpace(harness.RuntimeSHA256) != "",
			Packages:       append([]string(nil), harness.Packages...),
			PassEnvNames:   append([]string(nil), harness.PassEnv...),
			PromptMode:     harness.PromptMode,
			CatalogCache:   strings.TrimSpace(harness.CatalogCache) != "",
			ProviderFiles:  len(harness.ProviderFiles) > 0,
		})
	}
	sort.Slice(harnesses, func(i, j int) bool { return harnesses[i].Name < harnesses[j].Name })
	return harnesses
}

func (s *Server) buildVerificationCatalog() []VerificationCatalog {
	profiles := make([]VerificationCatalog, 0, len(s.cfg.Factory.Verification))
	for name, profile := range s.cfg.Factory.Verification {
		steps := make([]VerificationStepCatalog, 0, len(profile.Steps))
		for _, step := range profile.Steps {
			steps = append(steps, VerificationStepCatalog{
				ID:          step.ID,
				Argv:        append([]string(nil), step.Argv...),
				Description: step.Description,
				Mandatory:   step.IsMandatory(),
				TimeoutMS:   millis(step.EffectiveTimeout()),
				Dir:         step.Dir,
			})
		}
		profiles = append(profiles, VerificationCatalog{Name: name, Steps: steps})
	}
	sort.Slice(profiles, func(i, j int) bool { return profiles[i].Name < profiles[j].Name })
	return profiles
}

func (s *Server) buildCatalog() CatalogResponse {
	_, _, info := s.index.snapshot()

	models := make([]ModelCatalog, 0, len(s.cfg.Factory.Models))
	for name, model := range s.cfg.Factory.Models {
		models = append(models, ModelCatalog{
			Name: name, Provider: model.Provider, Model: model.Model,
			BaseURL: model.BaseURL, APIKeyEnv: model.APIKeyEnv, Scopes: append([]string(nil), model.Scopes...),
		})
	}
	sort.Slice(models, func(i, j int) bool { return models[i].Name < models[j].Name })

	egress := make([]EgressCatalog, 0, len(s.cfg.Factory.Egress))
	for name, policy := range s.cfg.Factory.Egress {
		egress = append(egress, EgressCatalog{
			Name: name, AllowInternet: policy.AllowInternet,
			AllowOut: append([]string(nil), policy.AllowOut...),
			DenyOut:  append([]string(nil), policy.DenyOut...),
			Summary:  policy.Summary(),
		})
	}
	sort.Slice(egress, func(i, j int) bool { return egress[i].Name < egress[j].Name })

	workspaces := make([]WorkspaceCatalog, 0, len(s.cfg.Factory.Workspaces))
	for name, workspace := range s.cfg.Factory.Workspaces {
		workspaces = append(workspaces, WorkspaceCatalog{
			Name: name, Template: workspace.Template,
			BasePackages:       append([]string(nil), workspace.BasePackages...),
			SnapshotConfigured: strings.TrimSpace(workspace.SnapshotID) != "",
			RepositoryCache:    workspace.RepositoryCache,
			SetupScriptPresent: strings.TrimSpace(workspace.SetupScript) != "",
		})
	}
	sort.Slice(workspaces, func(i, j int) bool { return workspaces[i].Name < workspaces[j].Name })

	budgets := make([]BudgetCatalog, 0, len(s.cfg.Factory.Budgets))
	for name, budget := range s.cfg.Factory.Budgets {
		budgets = append(budgets, BudgetCatalog{
			Name: name, MaxCostUSD: budget.MaxCostUSD, MaxTokens: budget.MaxTokens,
			MaxWallClockMS: millis(budget.MaxWallClock.Std()),
		})
	}
	sort.Slice(budgets, func(i, j int) bool { return budgets[i].Name < budgets[j].Name })

	scopes := make([]ScopeCatalog, 0, len(s.cfg.Factory.Scopes))
	for name, scope := range s.cfg.Factory.Scopes {
		scopes = append(scopes, ScopeCatalog{
			Name: name, Repositories: append([]string(nil), scope.Repositories...),
			Harnesses: append([]string(nil), scope.Harnesses...),
			Egress:    scope.Egress, Model: scope.Model, Workspace: scope.Workspace,
			Budget: scope.Budget, Review: scope.Review,
		})
	}
	sort.Slice(scopes, func(i, j int) bool { return scopes[i].Name < scopes[j].Name })

	return CatalogResponse{
		Environment:    s.cfg.Environment,
		ConfigPath:     s.cfg.ConfigPath,
		SchemaVersion:  s.cfg.Factory.SchemaVersion,
		FactoryVersion: factoryVersion(s.cfg.Factory),
		ConsoleVersion: s.cfg.Version,
		GeneratedAt:    s.now().UTC(),
		Cube:           s.buildCubeInfo(),
		Temporal:       s.buildTemporalInfo(),
		Storage:        s.buildStorageInfo(info.Runs, info.Changes),
		Limits:         s.buildLimitsCatalog(),
		Sandbox:        s.buildSandboxCatalog(),
		Harnesses:      s.buildHarnessCatalog(),
		Models:         models,
		Egress:         egress,
		Workspaces:     workspaces,
		Budgets:        budgets,
		Scopes:         scopes,
		Verification:   s.buildVerificationCatalog(),
		Review:         s.buildReviewCatalog(),
		Hardening:      s.buildHardeningCatalog(),
		Intake:         s.buildIntakeCatalog(),
		Observability:  s.buildObservabilityInfo(),
		Repositories:   append([]string(nil), s.cfg.Factory.Repositories.Allowed...),
		Warnings:       append([]string(nil), s.configWarnings...),
	}
}

func factoryVersion(cfg factory.Config) string {
	if strings.TrimSpace(cfg.FactoryVersion) != "" {
		return cfg.FactoryVersion
	}
	return factory.Version
}

func (s *Server) buildOperations(ctx context.Context) OperationsResponse {
	orderedRuns, _, info := s.snapshot()
	live := s.live.openRuns(ctx)
	sandboxes := s.live.sandboxes(ctx)
	probes := s.reachabilityProbes(ctx)
	catalog := s.buildCatalog()

	cleanup := buildCleanupAggregate(orderedRuns)
	liveState := s.buildLiveSandboxes(orderedRuns, live, sandboxes)

	warnings := append([]string(nil), s.configWarnings...)
	if !live.Available && s.cfg.Live {
		warnings = append(warnings, "Live orchestration unavailable: "+live.Error)
	}
	notes := []string{}
	if live.Available {
		notes = append(notes, fmt.Sprintf("Temporal reports %d open workflow(s).", len(live.Runs)))
	}
	if sandboxes.Available {
		notes = append(notes, fmt.Sprintf("CubeSandbox reports %d sandbox(es).", len(sandboxes.Sandboxes)))
	}

	storageReadable := true
	storageError := ""
	if stat, err := os.Stat(s.root); err != nil || !stat.IsDir() {
		storageReadable = false
		storageError = "The evidence store is not a readable directory."
	}

	return OperationsResponse{
		GeneratedAt:   s.now().UTC(),
		Environment:   s.cfg.Environment,
		Cleanup:       cleanup,
		LiveSandboxes: liveState,
		Infrastructure: InfrastructureInfo{
			Cube:          catalog.Cube,
			Temporal:      catalog.Temporal,
			Observability: catalog.Observability,
			Storage:       catalog.Storage,
		},
		Harnesses:    catalog.Harnesses,
		Verification: catalog.Verification,
		Limits:       catalog.Limits,
		Review:       catalog.Review,
		Hardening:    catalog.Hardening,
		Probes:       probes,
		DataHealth: DataHealth{
			Index:           info,
			StorageReadable: storageReadable,
			StorageError:    storageError,
			Notes:           notes,
		},
		Warnings: warnings,
	}
}

func buildCleanupAggregate(runs []*runRecord) CleanupAggregate {
	aggregate := CleanupAggregate{RecentFailures: []CleanupFailure{}}
	for _, record := range runs {
		manifest := record.Manifest
		cleanup := manifest.CleanupResult
		if !cleanup.Attempted && strings.TrimSpace(manifest.SandboxID) == "" {
			continue
		}
		aggregate.RunsWithSandbox++
		aggregate.RemainingTotal += cleanup.Remaining
		if cleanup.Verified && cleanup.Remaining == 0 {
			aggregate.Verified++
			continue
		}
		aggregate.Failed++
		errorText, _ := truncateText(cleanup.Error, maxErrorText)
		aggregate.RecentFailures = append(aggregate.RecentFailures, CleanupFailure{
			RunID: record.ID, SandboxID: manifest.SandboxID, Outcome: string(cleanup.Outcome),
			Verified: cleanup.Verified, Remaining: cleanup.Remaining, Error: errorText,
			FinishedAt: cleanup.FinishedAt, Repository: manifest.Repository,
			FactoryState: string(manifest.FactoryResult),
		})
	}
	sort.Slice(aggregate.RecentFailures, func(i, j int) bool {
		return aggregate.RecentFailures[i].FinishedAt.After(aggregate.RecentFailures[j].FinishedAt)
	})
	if len(aggregate.RecentFailures) > 15 {
		aggregate.RecentFailures = aggregate.RecentFailures[:15]
	}
	return aggregate
}

func (s *Server) buildLiveSandboxes(runs []*runRecord, live liveSnapshot, snapshot sandboxSnapshot) LiveSandboxState {
	state := LiveSandboxState{
		Sandboxes: []LiveSandbox{}, Available: snapshot.Available, Error: snapshot.Error,
		CheckedAt: snapshot.CheckedAt, AttributionAvailable: live.Available,
	}
	if !snapshot.Available {
		return state
	}
	if !live.Available {
		state.Note = "Workflow attribution is unavailable (" + live.Error + "); sandbox ownership is listed but leak classification is withheld."
	}
	open := map[string]LiveRun{}
	for _, run := range live.Runs {
		open[run.RunID] = run
	}
	byRun := map[string]*runRecord{}
	for _, record := range runs {
		byRun[record.ID] = record
	}
	for _, info := range snapshot.Sandboxes {
		item := LiveSandbox{
			ID:        info.ID,
			Template:  info.Template,
			State:     info.State,
			StartedAt: info.StartedAt.UTC(),
		}
		if info.Metadata != nil {
			item.RunID = info.Metadata["run_id"]
			item.WorkflowRunID = info.Metadata["workflow_run_id"]
			item.FactoryOwned = info.Metadata["origin"] == "factory"
		}
		_, openRun := open[item.RunID]
		record, recorded := byRun[item.RunID]
		switch {
		case !live.Available:
			// Classification needs open-workflow state. With Temporal
			// unreachable an owned sandbox could belong to a live run, so it is
			// neither active nor leaked until attribution is possible.
			item.AttributionKnown = false
		case openRun:
			item.AttributionKnown = true
			item.Active = true
			state.Active++
		case item.FactoryOwned && recorded:
			// The run has a durable manifest with a verified cleanup, yet the
			// sandbox is still listed. That is a leak, not an active run.
			item.AttributionKnown = true
			item.Leaked = true
			state.Leaked++
		case item.FactoryOwned && !recorded:
			// A factory-owned sandbox with no manifest and no open workflow is
			// unattributed; report it rather than guessing.
			item.AttributionKnown = true
			item.Leaked = true
			state.Leaked++
		default:
			item.AttributionKnown = true
		}
		if item.FactoryOwned {
			state.FactoryOwned++
		}
		_ = record
		state.Sandboxes = append(state.Sandboxes, item)
	}
	state.Total = len(state.Sandboxes)
	sort.Slice(state.Sandboxes, func(i, j int) bool { return state.Sandboxes[i].StartedAt.After(state.Sandboxes[j].StartedAt) })
	if len(state.Sandboxes) > 100 {
		state.Sandboxes = state.Sandboxes[:100]
	}
	return state
}
