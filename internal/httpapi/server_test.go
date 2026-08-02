package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hakangit/knowledge-commons/internal/identity"
	"github.com/hakangit/knowledge-commons/internal/knowledge"
)

type readinessStub struct {
	err error
}

func (stub readinessStub) Ping(context.Context) error {
	return stub.err
}

type operationsStub struct {
	proposal knowledge.Proposal
	err      error
	proposed *knowledge.ProposalDraft
	reviewed *knowledge.Review
}

type visibleOperationsStub struct {
	operationsStub
	includeRestricted bool
}

func (stub *visibleOperationsStub) GetPublishedVisible(context.Context, string, string, bool) (knowledge.Revision, error) {
	return knowledge.Revision{}, stub.err
}

func (stub *visibleOperationsStub) SearchVisible(_ context.Context, _ string, _ string, _ int, includeRestricted bool) ([]knowledge.SearchResult, error) {
	stub.includeRestricted = includeRestricted
	return []knowledge.SearchResult{}, stub.err
}

type sourceOperationsStub struct {
	draft *knowledge.SourceDraft
}

func (stub sourceOperationsStub) ImportSource(_ context.Context, draft knowledge.SourceDraft) (knowledge.SourceResult, error) {
	if stub.draft != nil {
		*stub.draft = draft
	}
	return knowledge.SourceResult{}, nil
}

func (stub operationsStub) Propose(_ context.Context, draft knowledge.ProposalDraft) (knowledge.Proposal, error) {
	if stub.proposed != nil {
		*stub.proposed = draft
	}
	return stub.proposal, stub.err
}

func (stub operationsStub) Review(_ context.Context, _ string, review knowledge.Review) (knowledge.ReviewResult, error) {
	if stub.reviewed != nil {
		*stub.reviewed = review
	}
	return knowledge.ReviewResult{}, stub.err
}

func (stub operationsStub) GetPublished(context.Context, string, string) (knowledge.Revision, error) {
	return knowledge.Revision{}, stub.err
}

func (stub operationsStub) Search(context.Context, string, string, int) ([]knowledge.SearchResult, error) {
	return nil, stub.err
}

func TestReadinessRejectsTrafficWhenStorageIsUnavailable(t *testing.T) {
	server := New(":0", "test", readinessStub{err: errors.New("database unavailable")}, operationsStub{}, identity.DisabledResolver{})
	request := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	response := httptest.NewRecorder()

	server.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", response.Code)
	}
}

func TestLivenessDoesNotDependOnStorage(t *testing.T) {
	server := New(":0", "test", readinessStub{err: errors.New("database unavailable")}, operationsStub{}, identity.DisabledResolver{})
	request := httptest.NewRequest(http.MethodGet, "/livez", nil)
	response := httptest.NewRecorder()

	server.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
}

func TestProposalEndpointRejectsUnknownFields(t *testing.T) {
	server := New(":0", "test", readinessStub{}, operationsStub{}, identity.HeaderResolver{})
	request := httptest.NewRequest(http.MethodPost, "/v1/proposals", strings.NewReader(`{"unexpected":true}`))
	request.Header.Set("X-KC-Actor", "assistant")
	response := httptest.NewRecorder()

	server.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", response.Code)
	}
}

func TestProposalEndpointUsesResolvedPrincipal(t *testing.T) {
	var proposed knowledge.ProposalDraft
	operations := operationsStub{proposal: knowledge.Proposal{ID: "proposal-1"}, proposed: &proposed}
	server := New(":0", "test", readinessStub{}, operations, identity.HeaderResolver{})
	request := httptest.NewRequest(http.MethodPost, "/v1/proposals", strings.NewReader(`{
		"knowledge_key":"handbook/onboarding",
		"canonical_language":"en",
		"language":"en",
		"title":"Onboarding checklist",
		"body":"Confirm account access before the first shift.",
		"evidence":[{"source":"task","reference":"https://tasks.example/api/v1/tasks/42"}]
	}`))
	request.Header.Set("X-KC-Actor", "assistant")
	response := httptest.NewRecorder()

	server.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	if proposed.ProposedBy != "assistant" {
		t.Fatalf("proposed by = %q", proposed.ProposedBy)
	}
	if len(proposed.Evidence) != 1 || proposed.Evidence[0].Source != "task" {
		t.Fatalf("evidence = %#v", proposed.Evidence)
	}
}

func TestProposalEndpointRejectsCallerControlledActor(t *testing.T) {
	server := New(":0", "test", readinessStub{}, operationsStub{}, identity.HeaderResolver{})
	request := httptest.NewRequest(http.MethodPost, "/v1/proposals", strings.NewReader(`{
		"knowledge_key":"handbook/onboarding",
		"canonical_language":"en",
		"language":"en",
		"proposed_by":"director",
		"title":"Onboarding checklist",
		"body":"Confirm account access before the first shift.",
		"evidence":[{"source":"task","reference":"https://tasks.example/api/v1/tasks/42"}]
	}`))
	request.Header.Set("X-KC-Actor", "assistant")
	response := httptest.NewRecorder()

	server.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", response.Code)
	}
}

func TestSearchRequiresIdentityBecauseSharedDoesNotMeanPublic(t *testing.T) {
	server := New(":0", "test", readinessStub{}, operationsStub{}, identity.DisabledResolver{})
	request := httptest.NewRequest(http.MethodGet, "/v1/search?q=temperature", nil)
	response := httptest.NewRecorder()

	server.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", response.Code)
	}
}

func TestRestrictedSearchUsesResolvedSubjectPolicy(t *testing.T) {
	operations := &visibleOperationsStub{}
	server := NewWithSources(
		":0", "test", readinessStub{}, operations, nil, identity.HeaderResolver{},
		NewAccessPolicy([]string{"admin"}, nil),
	)
	request := httptest.NewRequest(http.MethodGet, "/v1/search?q=restricted", nil)
	request.Header.Set("X-KC-Actor", "admin")
	response := httptest.NewRecorder()

	server.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK || !operations.includeRestricted {
		t.Fatalf("status = %d include restricted = %v", response.Code, operations.includeRestricted)
	}
}

func TestSourceIngestionRequiresConfiguredSubject(t *testing.T) {
	var imported knowledge.SourceDraft
	server := NewWithSources(
		":0", "test", readinessStub{}, operationsStub{}, sourceOperationsStub{draft: &imported},
		identity.HeaderResolver{}, NewAccessPolicy(nil, []string{"admin"}),
	)
	body := `{"knowledge_key":"training/jet","canonical_language":"en","language":"en","title":"Jet","body":"Reach target temperature.","visibility":"shared","source_key":"training","source_path":"content/en/jet.md","source_revision":"abc123","source_url":"https://git.example/jet"}`

	denied := httptest.NewRequest(http.MethodPut, "/v1/source-documents", strings.NewReader(body))
	denied.Header.Set("X-KC-Actor", "viewer")
	deniedResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(deniedResponse, denied)
	if deniedResponse.Code != http.StatusForbidden {
		t.Fatalf("denied status = %d", deniedResponse.Code)
	}

	allowed := httptest.NewRequest(http.MethodPut, "/v1/source-documents", strings.NewReader(body))
	allowed.Header.Set("X-KC-Actor", "admin")
	allowedResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(allowedResponse, allowed)
	if allowedResponse.Code != http.StatusOK || imported.ImportedBy != "admin" {
		t.Fatalf("allowed status = %d imported by = %q", allowedResponse.Code, imported.ImportedBy)
	}
}
