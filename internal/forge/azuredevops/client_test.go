package azuredevops

import "testing"

func TestAPIURLEncodesEverySegment(t *testing.T) {
	t.Parallel()

	client := newTestClient(t, "https://dev.azure.com/acme", RetryPolicy{MaxAttempts: 1})
	endpoint := client.apiURL("Payments Team", "_apis", "git", "repositories", "payments/repo", "pullRequests")
	want := "https://dev.azure.com/acme/Payments%20Team/_apis/git/repositories/payments%2Frepo/pullRequests"
	if got := endpoint.String(); got != want {
		t.Fatalf("apiURL = %q, want %q", got, want)
	}
}
