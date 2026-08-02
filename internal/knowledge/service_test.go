package knowledge

import (
	"context"
	"errors"
	"testing"
)

type repositoryStub struct {
	createDraft ProposalDraft
	review      Review
	createErr   error
	reviewErr   error
}

func (stub *repositoryStub) CreateProposal(_ context.Context, draft ProposalDraft) (Proposal, error) {
	stub.createDraft = draft
	return Proposal{ID: "proposal-1", KnowledgeKey: draft.KnowledgeKey, Status: "pending", ProposedBy: draft.ProposedBy}, stub.createErr
}

func (stub *repositoryStub) ReviewProposal(_ context.Context, _ string, review Review) (ReviewResult, error) {
	stub.review = review
	return ReviewResult{ProposalID: "proposal-1", Status: "accepted"}, stub.reviewErr
}

func (stub *repositoryStub) GetPublished(context.Context, string, string) (Revision, error) {
	return Revision{}, ErrNotFound
}

func (stub *repositoryStub) Search(context.Context, string, string, int) ([]SearchResult, error) {
	return nil, nil
}

func TestProposalRequiresEvidenceBecausePublishedKnowledgeMustBeAuditable(t *testing.T) {
	service := NewService(&repositoryStub{})
	_, err := service.Propose(context.Background(), ProposalDraft{
		KnowledgeKey: "dye-house/shade-correction", CanonicalLanguage: "vi", Language: "vi",
		ProposedBy: "assistant", Title: "Shade correction", Body: "Procedure",
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected invalid error, got %v", err)
	}
}

func TestProposalNormalizesItsStableKnowledgeIdentity(t *testing.T) {
	repository := &repositoryStub{}
	service := NewService(repository)
	_, err := service.Propose(context.Background(), ProposalDraft{
		KnowledgeKey: " /Dye-House/Shade-Correction/ ", CanonicalLanguage: "VI", Language: "VI",
		ProposedBy: "assistant", Title: "Shade correction", Body: "Procedure",
		Evidence: []Evidence{{Source: "work-order", Reference: "WO-123"}},
	})
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	if repository.createDraft.KnowledgeKey != "dye-house/shade-correction" {
		t.Fatalf("knowledge key = %q", repository.createDraft.KnowledgeKey)
	}
}

func TestReviewRequiresAnExplicitReason(t *testing.T) {
	service := NewService(&repositoryStub{})
	_, err := service.Review(context.Background(), "proposal-1", Review{Decision: "accept", ReviewedBy: "editor"})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected invalid error, got %v", err)
	}
}
