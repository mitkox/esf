package factory

import (
	"fmt"

	"github.com/mitkox/esf/internal/forge"
)

// AppendIntakeRejection records an intake decision that starts no run.
//
// A rejection happens before a work-item workflow exists, so it has no run
// artifact of its own. It is appended to the shared intake audit trail instead,
// keeping the same action and outcome names an accepted decision uses.
func AppendIntakeRejection(dataDir string, rejection forge.Rejection) error {
	store, err := openIntakeStore(dataDir)
	if err != nil {
		return err
	}
	entry := AuditEntry{
		Action:    AuditIntake,
		Actor:     rejection.Actor.Descriptor,
		ActorKind: ActorKindForge,
		Outcome:   AuditOutcomeRejected,
		Detail: map[string]any{
			"connector":    rejection.Connector,
			"work_item_id": rejection.WorkItemID,
			"revision":     rejection.Revision,
			"status":       "rejected",
			"reason":       rejection.Reason,
		},
	}
	if err := appendIntakeAudit(store, entry, nil); err != nil {
		return fmt.Errorf("intake audit: %w", err)
	}
	return nil
}
