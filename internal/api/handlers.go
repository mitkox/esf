package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
)

// Controller is the execution control exposed by the API. The factory command
// implements it over Temporal and the durable artifact store.
type Controller interface {
	// Submit starts a run, or returns the existing run for the same identity.
	Submit(ctx context.Context, request RunRequest) (RunStatus, error)
	// Status reports a run's current state.
	Status(ctx context.Context, runID string) (RunStatus, error)
	// Events reports the run's current condition snapshots after a cursor.
	Events(ctx context.Context, runID string, after int) ([]Event, error)
	// Evidence returns the run's durable manifest as stored.
	Evidence(ctx context.Context, runID string) ([]byte, error)
	// Cancel requests cancellation and reports the run's current state.
	Cancel(ctx context.Context, runID string) (RunStatus, error)
}

var (
	// ErrRunNotFound means the request names a run the API does not know.
	ErrRunNotFound = errors.New("run not found")
	// ErrInvalidRequest means the submitted request cannot be honoured.
	ErrInvalidRequest = errors.New("invalid run request")
	// ErrRunConflict means the run identity is already executing a different
	// request, so the submission cannot be treated as the same activation.
	ErrRunConflict = errors.New("run conflict")
	// ErrRunTerminal means the run identity is closed and cannot be restarted.
	ErrRunTerminal = errors.New("run is terminal")
)

const (
	codeUnauthorized    = "unauthorized"
	codeInvalidRequest  = "invalid_request"
	codeRunNotFound     = "run_not_found"
	codeRunConflict     = "run_conflict"
	codeRunTerminal     = "run_terminal"
	codeRequestTooLarge = "request_too_large"
	codeInternal        = "internal"
)

// maxRequestBodyBytes bounds one submission body. The decoder streams through
// http.MaxBytesReader, so an oversized request is refused without buffering it.
const maxRequestBodyBytes = 1 << 20

func (s *Server) handleSubmit(response http.ResponseWriter, request *http.Request) {
	var submission RunRequest
	if err := decodeBody(response, request, &submission); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(response, http.StatusRequestEntityTooLarge, codeRequestTooLarge, "The request body exceeds the API limit.")
			return
		}
		writeError(response, http.StatusBadRequest, codeInvalidRequest, "The request body must be one JSON run request with no unknown fields.")
		return
	}
	status, err := s.controller.Submit(request.Context(), submission)
	if err != nil {
		s.writeControllerError(response, err)
		return
	}
	writeJSON(response, http.StatusAccepted, status)
}

func (s *Server) handleStatus(response http.ResponseWriter, request *http.Request) {
	status, err := s.controller.Status(request.Context(), request.PathValue("run_id"))
	if err != nil {
		s.writeControllerError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, status)
}

func (s *Server) handleEvents(response http.ResponseWriter, request *http.Request) {
	after := 0
	if raw := request.URL.Query().Get("after"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 {
			writeError(response, http.StatusBadRequest, codeInvalidRequest, "The after cursor must be a non-negative integer.")
			return
		}
		after = parsed
	}
	events, err := s.controller.Events(request.Context(), request.PathValue("run_id"), after)
	if err != nil {
		s.writeControllerError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, events)
}

func (s *Server) handleEvidence(response http.ResponseWriter, request *http.Request) {
	manifest, err := s.controller.Evidence(request.Context(), request.PathValue("run_id"))
	if err != nil {
		s.writeControllerError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, Evidence{RunID: request.PathValue("run_id"), Manifest: manifest})
}

func (s *Server) handleCancel(response http.ResponseWriter, request *http.Request) {
	status, err := s.controller.Cancel(request.Context(), request.PathValue("run_id"))
	if err != nil {
		s.writeControllerError(response, err)
		return
	}
	writeJSON(response, http.StatusAccepted, status)
}

func (s *Server) handleNotFound(response http.ResponseWriter, _ *http.Request) {
	writeError(response, http.StatusNotFound, "not_found", "No such execution API endpoint.")
}

func (s *Server) writeControllerError(response http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrInvalidRequest):
		writeError(response, http.StatusBadRequest, codeInvalidRequest, err.Error())
	case errors.Is(err, ErrRunNotFound):
		writeError(response, http.StatusNotFound, codeRunNotFound, "The run does not exist.")
	case errors.Is(err, ErrRunConflict):
		writeError(response, http.StatusConflict, codeRunConflict,
			"The run identity is already executing a different request.")
	case errors.Is(err, ErrRunTerminal):
		writeError(response, http.StatusConflict, codeRunTerminal,
			"The run is already closed and cannot be resubmitted.")
	default:
		writeError(response, http.StatusInternalServerError, codeInternal, "The execution API could not complete the request.")
	}
}

// decodeBody accepts exactly one bounded JSON value and rejects unknown fields,
// so a typo or a field the contract does not carry cannot be silently ignored.
func decodeBody(response http.ResponseWriter, request *http.Request, value any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, maxRequestBodyBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("expected one JSON value")
	}
	return nil
}

func writeJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("X-Content-Type-Options", "nosniff")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}

func writeError(response http.ResponseWriter, status int, code, message string) {
	writeJSON(response, status, errorResponse{Error: message, Code: code})
}
