package main

import (
	"context"
	"errors"
	"testing"

	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"

	"github.com/mitkox/esf/internal/api"
	"github.com/mitkox/esf/internal/factory"
	"github.com/mitkox/esf/internal/repository"
)

func apiRequest() api.RunRequest {
	return api.RunRequest{
		RunID:      "run-1",
		Repository: "https://example.com/acme/repo",
		Revision:   "main",
		Task:       "fix the bug",
	}
}

func TestAPISubmitClosedRunIsTypedTerminal(t *testing.T) {
	t.Parallel()

	controller := &apiController{
		config: factory.Default(),
		startWorkflow: func(context.Context, client.StartWorkflowOptions, factory.RunRequest) error {
			return &serviceerror.WorkflowExecutionAlreadyStarted{Message: "already started"}
		},
	}
	_, err := controller.Submit(context.Background(), apiRequest())
	if !errors.Is(err, api.ErrRunTerminal) {
		t.Fatalf("Submit error = %v, want ErrRunTerminal", err)
	}
	if errors.Is(err, api.ErrRunConflict) {
		t.Fatalf("a closed run was reported as a conflict: %v", err)
	}
}

func TestAPISubmitReusedRunRejectsDifferentRequest(t *testing.T) {
	t.Parallel()

	recorded := factory.RunStatus{
		RunID:      "run-1",
		State:      factory.StateRunning,
		Repository: "https://example.com/acme/other",
		Revision:   "release",
		TaskHash:   repository.HashTask("the recorded task"),
	}
	controller := &apiController{
		config: factory.Default(),
		startWorkflow: func(context.Context, client.StartWorkflowOptions, factory.RunRequest) error {
			return nil
		},
		queryStatus: func(context.Context, string) (factory.RunStatus, error) {
			return recorded, nil
		},
	}
	_, err := controller.Submit(context.Background(), apiRequest())
	if !errors.Is(err, api.ErrRunConflict) {
		t.Fatalf("Submit error = %v, want ErrRunConflict", err)
	}
}

func TestAPISubmitReusedRunReturnsRecordedStatus(t *testing.T) {
	t.Parallel()

	request := apiRequest()
	recorded := factory.RunStatus{
		RunID:      request.RunID,
		State:      factory.StateRunning,
		Repository: request.Repository,
		Revision:   request.Revision,
		TaskHash:   repository.HashTask(request.Task),
	}
	controller := &apiController{
		config: factory.Default(),
		startWorkflow: func(context.Context, client.StartWorkflowOptions, factory.RunRequest) error {
			return nil
		},
		queryStatus: func(context.Context, string) (factory.RunStatus, error) {
			return recorded, nil
		},
	}
	got, err := controller.Submit(context.Background(), request)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if got.State != string(factory.StateRunning) {
		t.Fatalf("state = %q, want the recorded RUNNING state", got.State)
	}
	if got.Repository != recorded.Repository || got.Revision != recorded.Revision {
		t.Fatalf("status = %#v, want the recorded request identity", got)
	}
}
