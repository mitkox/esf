package factory

import (
	"context"
	"github.com/mitkox/esf/internal/securityassessment"
)

type cubeSecurityAssessmentExecutor struct {
	executor *securityassessment.CubeExecutor
}

// NewCubeSecurityAssessmentExecutor is explicit operator wiring. An assessment
// request cannot override the guard, driver, target entrypoint, or Cube controls.
func NewCubeSecurityAssessmentExecutor(config securityassessment.CubeExecutorConfig) (SecurityAssessmentExecutor, error) {
	executor, err := securityassessment.NewCubeExecutor(config)
	if err != nil {
		return nil, err
	}
	return &cubeSecurityAssessmentExecutor{executor: executor}, nil
}

func (e *cubeSecurityAssessmentExecutor) Assess(ctx context.Context, in SecurityAssessmentExecution) (SecurityAssessmentExecutionResult, error) {
	result, err := e.executor.Assess(ctx, securityassessment.CubeAssessmentRequest{RunID: in.RunID, ChangeID: in.ChangeID, ParentRunID: in.ParentRunID, Profile: in.Profile, Candidate: in.Candidate})
	return SecurityAssessmentExecutionResult{Profile: result.Profile, Expected: result.Expected, Manifest: result.Manifest, PrivateEvidence: result.PrivateEvidence, ExpectedHashes: result.ExpectedHashes}, err
}
