package azuredevops

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/mitkox/esf/internal/forge"
)

const (
	defaultWebhookBodyBytes = 1 << 20
	maxWebhookBodyBytes     = 8 << 20
	defaultDedupeLimit      = 4096
	reasonNoMapping         = "no operator mapping"
	reasonActorUnavailable  = "actor identity unavailable"
	reasonActorUnauthorized = "actor lacks repository write permission"
)

var defaultWatchedFields = []string{
	"System.TeamProject",
	"System.AreaPath",
	"System.WorkItemType",
	"System.State",
}

// WebhookConfig configures the authenticated Azure DevOps service-hook
// receiver.
type WebhookConfig struct {
	// Connector is the audit name. It defaults to the client's name.
	Connector string
	// Sink receives proposals and audited rejections.
	Sink forge.IntakeSink
	// Repositories maps work-item attributes to the operator-selected
	// repository. The payload cannot supply it.
	Repositories forge.RepositoryResolver
	// WatchedFields are the work-item fields the operator mapping uses. An
	// update that changes none of them does not start intake. Empty uses the
	// project, area, type, and state defaults; a label-based rule must add
	// System.Tags explicitly.
	WatchedFields []string
	// MaxBodyBytes bounds the delivered body. Zero uses 1 MiB.
	MaxBodyBytes int64
	// DedupeLimit bounds remembered delivery identities. Zero uses 4096.
	DedupeLimit int
}

// Webhook is an http.Handler for Azure DevOps workitem.updated deliveries.
type Webhook struct {
	client       *Client
	connector    string
	sink         forge.IntakeSink
	repositories forge.RepositoryResolver
	watched      map[string]struct{}
	maxBody      int64
	dedupe       *deduper

	accepted     atomic.Uint64
	duplicates   atomic.Uint64
	filtered     atomic.Uint64
	rejected     atomic.Uint64
	unauthorized atomic.Uint64
}

// WebhookStats is a point-in-time count of delivery outcomes.
type WebhookStats struct {
	Accepted     uint64
	Duplicates   uint64
	Filtered     uint64
	Rejected     uint64
	Unauthorized uint64
}

// NewWebhook validates and constructs a webhook receiver.
func NewWebhook(client *Client, cfg WebhookConfig) (*Webhook, error) {
	if client == nil {
		return nil, fmt.Errorf("azuredevops: webhook client is required")
	}
	if cfg.Sink == nil {
		return nil, fmt.Errorf("azuredevops: webhook sink is required")
	}
	if cfg.Repositories == nil {
		return nil, fmt.Errorf("azuredevops: webhook repository resolver is required")
	}
	connector := strings.TrimSpace(cfg.Connector)
	if connector == "" {
		connector = client.Name()
	}
	if connector == "" {
		return nil, fmt.Errorf("azuredevops: webhook connector name is required")
	}
	maxBody := cfg.MaxBodyBytes
	if maxBody == 0 {
		maxBody = defaultWebhookBodyBytes
	}
	if maxBody < 0 || maxBody > maxWebhookBodyBytes {
		return nil, fmt.Errorf("azuredevops: webhook body limit must be between 1 and %d bytes", maxWebhookBodyBytes)
	}
	dedupeLimit := cfg.DedupeLimit
	if dedupeLimit == 0 {
		dedupeLimit = defaultDedupeLimit
	}
	if dedupeLimit < 0 {
		return nil, fmt.Errorf("azuredevops: webhook dedupe limit must be positive")
	}

	watched := make(map[string]struct{})
	for _, field := range cfg.WatchedFields {
		if name := strings.TrimSpace(field); name != "" {
			watched[strings.ToLower(name)] = struct{}{}
		}
	}
	if len(watched) == 0 {
		for _, field := range defaultWatchedFields {
			watched[strings.ToLower(field)] = struct{}{}
		}
	}
	return &Webhook{
		client:       client,
		connector:    connector,
		sink:         cfg.Sink,
		repositories: cfg.Repositories,
		watched:      watched,
		maxBody:      maxBody,
		dedupe:       newDeduper(dedupeLimit),
	}, nil
}

// Stats returns the delivery counters.
func (h *Webhook) Stats() WebhookStats {
	return WebhookStats{
		Accepted:     h.accepted.Load(),
		Duplicates:   h.duplicates.Load(),
		Filtered:     h.filtered.Load(),
		Rejected:     h.rejected.Load(),
		Unauthorized: h.unauthorized.Load(),
	}
}

// ServeHTTP authenticates and bounds the delivery before parsing, then treats
// the payload as a hint and re-fetches the authoritative work item.
func (h *Webhook) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeWebhookError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	// Credentials are checked before the body is read or parsed.
	if !h.client.authorized(r) {
		h.unauthorized.Add(1)
		w.Header().Set("WWW-Authenticate", `Basic realm="esf-azuredevops-webhook"`)
		writeWebhookError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if r.ContentLength > h.maxBody {
		writeWebhookError(w, http.StatusRequestEntityTooLarge, "body_too_large")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, h.maxBody))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeWebhookError(w, http.StatusRequestEntityTooLarge, "body_too_large")
			return
		}
		writeWebhookError(w, http.StatusBadRequest, "body_unreadable")
		return
	}

	var envelope webhookEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		writeWebhookError(w, http.StatusBadRequest, "invalid_payload")
		return
	}
	if envelope.EventType != "workitem.updated" ||
		(envelope.PublisherID != "tfs" && envelope.PublisherID != "azure-devops") {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	identity := strings.TrimSpace(envelope.ID)
	if identity == "" && envelope.NotificationID > 0 {
		identity = "notification:" + strconv.FormatInt(envelope.NotificationID, 10)
	}
	workItemID := envelope.Resource.WorkItemID
	if workItemID <= 0 {
		workItemID = envelope.Resource.ID
	}
	revision := envelope.Resource.Rev
	if revision <= 0 && envelope.Resource.UpdateRevision != nil {
		revision = envelope.Resource.UpdateRevision.Rev
	}
	if identity == "" || workItemID <= 0 || revision <= 0 {
		writeWebhookError(w, http.StatusBadRequest, "invalid_payload")
		return
	}

	dedupeKey := identity + "\x00" + strconv.FormatInt(revision, 10)
	changedFields := changedFieldNames(envelope.Resource.Fields)
	if !h.watchedChange(changedFields) {
		h.filtered.Add(1)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	// Claim before any authoritative read so concurrent copies of one delivery
	// cannot both propose. A failed attempt releases the claim for a retry.
	if !h.dedupe.Claim(dedupeKey) {
		h.duplicates.Add(1)
		w.WriteHeader(http.StatusNoContent)
		return
	}

	snapshot, err := h.client.FetchWorkItem(r.Context(), workItemID)
	if err != nil {
		h.dedupe.Release(dedupeKey)
		writeWebhookError(w, http.StatusBadGateway, "forge_unavailable")
		return
	}
	repository, err := h.repositories.ResolveRepository(snapshot.WorkItem.MappingFields())
	if err != nil {
		if err := h.reject(r.Context(), snapshot.WorkItem, snapshot.Actor, reasonNoMapping); err != nil {
			h.dedupe.Release(dedupeKey)
			writeWebhookError(w, http.StatusInternalServerError, "audit_unavailable")
			return
		}
		writeWebhookError(w, http.StatusForbidden, "rejected")
		return
	}
	actor := snapshot.Actor
	if strings.TrimSpace(actor.Descriptor) == "" {
		if err := h.reject(r.Context(), snapshot.WorkItem, actor, reasonActorUnavailable); err != nil {
			h.dedupe.Release(dedupeKey)
			writeWebhookError(w, http.StatusInternalServerError, "audit_unavailable")
			return
		}
		writeWebhookError(w, http.StatusForbidden, "rejected")
		return
	}
	allowed, err := h.client.RepositoryWrite(r.Context(), repository, actor.Descriptor)
	if err != nil {
		h.dedupe.Release(dedupeKey)
		writeWebhookError(w, http.StatusBadGateway, "forge_unavailable")
		return
	}
	if !allowed {
		if err := h.reject(r.Context(), snapshot.WorkItem, actor, reasonActorUnauthorized); err != nil {
			h.dedupe.Release(dedupeKey)
			writeWebhookError(w, http.StatusInternalServerError, "audit_unavailable")
			return
		}
		writeWebhookError(w, http.StatusForbidden, "rejected")
		return
	}

	proposal := forge.Proposal{
		Candidate: forge.Candidate{
			Connector:    h.connector,
			Source:       forge.SourceWebhook,
			WorkItem:     snapshot.WorkItem,
			WorkItemHash: snapshot.WorkItemHash,
		},
		Actor:         actor,
		DeliveryID:    identity,
		ChangedFields: changedFields,
		// The proposal is evidence that the repository write check passed.
		RepositoryPermission: forge.PermissionWrite,
	}
	if err := h.sink.Propose(r.Context(), proposal); err != nil {
		h.dedupe.Release(dedupeKey)
		writeWebhookError(w, http.StatusInternalServerError, "proposal_failed")
		return
	}
	h.accepted.Add(1)
	writeWebhookJSON(w, http.StatusAccepted, map[string]string{"status": "accepted"})
}

func (h *Webhook) watchedChange(changed []string) bool {
	for _, field := range changed {
		if _, ok := h.watched[strings.ToLower(field)]; ok {
			return true
		}
	}
	return false
}

func (h *Webhook) reject(ctx context.Context, workItem forge.WorkItem, actor forge.Actor, reason string) error {
	if err := h.sink.Reject(ctx, forge.Rejection{
		Connector:  h.connector,
		WorkItemID: workItem.ID,
		Revision:   workItem.Revision,
		Actor:      actor,
		Reason:     reason,
	}); err != nil {
		return err
	}
	h.rejected.Add(1)
	return nil
}

func changedFieldNames(fields map[string]json.RawMessage) []string {
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// authorized checks the single Basic Authorization header with constant-time
// comparisons against the connector's configured credentials.
func (c *Client) authorized(r *http.Request) bool {
	values := r.Header.Values("Authorization")
	if len(values) != 1 {
		return false
	}
	scheme, encoded, ok := strings.Cut(strings.TrimSpace(values[0]), " ")
	if !ok || !strings.EqualFold(scheme, "Basic") {
		return false
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil {
		return false
	}
	username, password, ok := strings.Cut(string(decoded), ":")
	if !ok {
		return false
	}
	expectedUsername, expectedPassword := splitBasicCredential(c.credential.Value())
	return constantTimeEqual(username, expectedUsername) && constantTimeEqual(password, expectedPassword)
}

func constantTimeEqual(a, b string) bool {
	aDigest := sha256.Sum256([]byte(a))
	bDigest := sha256.Sum256([]byte(b))
	return subtle.ConstantTimeCompare(aDigest[:], bDigest[:]) == 1
}

type webhookEnvelope struct {
	ID             string `json:"id"`
	NotificationID int64  `json:"notificationId"`
	EventType      string `json:"eventType"`
	PublisherID    string `json:"publisherId"`
	Resource       struct {
		ID             int64                      `json:"id"`
		WorkItemID     int64                      `json:"workItemId"`
		Rev            int64                      `json:"rev"`
		Fields         map[string]json.RawMessage `json:"fields"`
		UpdateRevision *struct {
			Rev int64 `json:"rev"`
		} `json:"revision"`
	} `json:"resource"`
}

type deduper struct {
	mu    sync.Mutex
	seen  map[string]struct{}
	order []string
	limit int
}

func newDeduper(limit int) *deduper {
	if limit <= 0 {
		limit = defaultDedupeLimit
	}
	return &deduper{seen: make(map[string]struct{}), limit: limit}
}

func (d *deduper) Claim(key string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.seen[key]; ok {
		return false
	}
	if len(d.order) >= d.limit {
		oldest := d.order[0]
		d.order = d.order[1:]
		delete(d.seen, oldest)
	}
	d.seen[key] = struct{}{}
	d.order = append(d.order, key)
	return true
}

func (d *deduper) Release(key string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.seen[key]; !ok {
		return
	}
	delete(d.seen, key)
	for i, item := range d.order {
		if item == key {
			d.order = append(d.order[:i], d.order[i+1:]...)
			return
		}
	}
}

func writeWebhookError(w http.ResponseWriter, status int, code string) {
	writeWebhookJSON(w, status, map[string]string{"error": code})
}

func writeWebhookJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
