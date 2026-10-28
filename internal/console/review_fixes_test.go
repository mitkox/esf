package console

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mitkox/esf/internal/factory"
	commonpb "go.temporal.io/api/common/v1"
	workflowpb "go.temporal.io/api/workflow/v1"
	"go.temporal.io/api/workflowservice/v1"
	temporalclient "go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestCompletedRunDetailHasOnlyReadCommands(t *testing.T) {
	root, clock := buildFixtureStore(t)
	server := newTestServer(t, root, clock, nil)
	status, body, _ := request(t, server, http.MethodGet, "/api/v1/console/runs/run-success", testToken)
	if status != http.StatusOK {
		t.Fatalf("status %d: %s", status, body)
	}
	var detail RunDetail
	decode(t, body, &detail)
	if detail.Actions.ApproveCommand != "" || detail.Actions.RejectCommand != "" || detail.Actions.CancelCommand != "" {
		t.Fatalf("completed run offers live commands: %+v", detail.Actions)
	}
	if detail.Actions.Describe == "" || detail.Actions.Logs == "" {
		t.Fatalf("missing read commands: %+v", detail.Actions)
	}
}

func TestChangeDetailUsesPausedActivation(t *testing.T) {
	root, clock := buildFixtureStore(t)
	s, err := New(Config{DataDir: root, ReadToken: testToken, Now: clock.Now, Live: true, Factory: factory.Default()})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.live.storeSnapshot(liveSnapshot{Available: true, Runs: []LiveRun{{RunID: "new$(run)", ChangeID: "change-success", State: string(factory.StatePaused)}}})
	server := httptest.NewServer(s.Handler())
	defer server.Close()
	status, body, _ := request(t, server, http.MethodGet, "/api/v1/console/changes/change-success", testToken)
	if status != http.StatusOK {
		t.Fatalf("status %d: %s", status, body)
	}
	var detail ChangeDetail
	decode(t, body, &detail)
	if !detail.Review.Pending || !detail.Review.Actionable || !detail.Review.Configured || detail.Review.ApproveCommand != "factory review 'new$(run)' --approve" {
		t.Fatalf("paused rerun gate missing: %+v", detail.Review)
	}
}

type blockedListClient struct {
	temporalclient.Client
	calls   atomic.Int32
	entered chan struct{}
	release chan struct{}
}

func (c *blockedListClient) ListWorkflow(ctx context.Context, _ *workflowservice.ListWorkflowExecutionsRequest) (*workflowservice.ListWorkflowExecutionsResponse, error) {
	c.calls.Add(1)
	c.entered <- struct{}{}
	select {
	case <-c.release:
		return &workflowservice.ListWorkflowExecutionsResponse{}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestConcurrentLiveRefreshIsShared(t *testing.T) {
	c := &blockedListClient{entered: make(chan struct{}, 20), release: make(chan struct{})}
	l := newLiveService(factory.Default(), true, time.Now)
	l.client = c
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if s := l.openRuns(context.Background()); !s.Available {
				t.Errorf("snapshot: %+v", s)
			}
		}()
	}
	<-c.entered
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if snapshot := l.openRuns(cancelled); snapshot.Available || snapshot.Error == "" {
		t.Fatalf("cancelled waiter: %+v", snapshot)
	}
	// Keep the backend blocked while all concurrent refresh requests arrive.
	time.Sleep(50 * time.Millisecond)
	close(c.release)
	wg.Wait()
	if got := c.calls.Load(); got != 1 {
		t.Fatalf("backend refreshes = %d, want 1", got)
	}
}

func TestConcurrentSandboxRefreshIsShared(t *testing.T) {
	var calls atomic.Int32
	entered := make(chan struct{}, 20)
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		entered <- struct{}{}
		<-release
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("[]"))
	}))
	defer server.Close()
	cfg := factory.Default()
	cfg.Cube.APIURL = server.URL
	cfg.Cube.TemplateID = "test-template"
	l := newLiveService(cfg, true, time.Now)
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if snapshot := l.sandboxes(context.Background()); !snapshot.Available {
				t.Errorf("snapshot: %+v", snapshot)
			}
		}()
	}
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		close(release)
		wg.Wait()
		t.Fatal("sandbox inventory did not reach the test backend")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if snapshot := l.sandboxes(cancelled); snapshot.Available || snapshot.Error == "" {
		t.Fatalf("cancelled waiter: %+v", snapshot)
	}
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()
	if got := calls.Load(); got != 1 {
		t.Fatalf("sandbox refreshes = %d, want 1", got)
	}
}

type unknownStatusClient struct{ temporalclient.Client }

func (*unknownStatusClient) ListWorkflow(context.Context, *workflowservice.ListWorkflowExecutionsRequest) (*workflowservice.ListWorkflowExecutionsResponse, error) {
	response := &workflowservice.ListWorkflowExecutionsResponse{}
	for i := range maxStatusQueries + 1 {
		response.Executions = append(response.Executions, &workflowpb.WorkflowExecutionInfo{
			Execution: &commonpb.WorkflowExecution{WorkflowId: factory.WorkflowIDForRun(fmt.Sprintf("run-%d", i))},
			StartTime: timestamppb.Now(),
		})
	}
	return response, nil
}

func (*unknownStatusClient) QueryWorkflow(context.Context, string, string, string, ...interface{}) (converter.EncodedValue, error) {
	return nil, errors.New("workflow query unavailable")
}

func TestFailedAndCappedLiveQueriesRemainUnknown(t *testing.T) {
	l := newLiveService(factory.Default(), true, time.Now)
	l.client = &unknownStatusClient{}
	snapshot := l.openRuns(context.Background())
	if !snapshot.Available || snapshot.UnknownRuns != maxStatusQueries+1 {
		t.Fatalf("snapshot: %+v", snapshot)
	}
	for _, run := range snapshot.Runs {
		if run.State != "UNKNOWN" || run.QueryError == "" {
			t.Fatalf("unqueried run looks known: %+v", run)
		}
	}
	root, clock := buildFixtureStore(t)
	s, err := New(Config{DataDir: root, ReadToken: testToken, Now: clock.Now, Live: true, Factory: factory.Default()})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.live.storeSnapshot(snapshot)
	server := httptest.NewServer(s.Handler())
	defer server.Close()
	_, body, _ := request(t, server, http.MethodGet, "/api/v1/console/reviews", testToken)
	var reviews ReviewsResponse
	decode(t, body, &reviews)
	if reviews.UnknownLiveRuns != maxStatusQueries+1 || reviews.PausedRuns != 0 {
		t.Fatalf("coverage lost: %+v", reviews)
	}
	attention := s.buildAttention(nil, nil, snapshot)
	if len(attention) == 0 || attention[0].ID != "review-coverage" {
		t.Fatalf("missing coverage warning: %+v", attention)
	}
	health := s.buildHealth(IndexInfo{}, snapshot, nil, nil, nil)
	for _, check := range health.Checks {
		if (check.ID == "orchestration" || check.ID == "reviews") && check.State != "warn" {
			t.Fatalf("unknown review coverage looks healthy: %+v", check)
		}
	}
}

func TestLiveRunActionsRespectState(t *testing.T) {
	for _, state := range []factory.RunState{factory.StateRunning, factory.StatePaused, factory.StateSucceeded} {
		actions := runActions("run", state)
		if (actions.ApproveCommand != "") != (state == factory.StatePaused) {
			t.Fatalf("review state %s: %+v", state, actions)
		}
		if (actions.CancelCommand != "") != (state != factory.StateSucceeded) {
			t.Fatalf("cancel state %s: %+v", state, actions)
		}
	}
}
