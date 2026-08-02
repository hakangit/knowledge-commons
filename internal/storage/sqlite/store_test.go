package sqlite

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/hakangit/knowledge-commons/internal/knowledge"
)

func TestPublishedKnowledgeSurvivesRestartAndRemainsSearchable(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "knowledge.db")
	store, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate store: %v", err)
	}

	proposal, err := store.CreateProposal(ctx, knowledge.ProposalDraft{
		KnowledgeKey:      "operations/release-check",
		CanonicalLanguage: "en",
		Language:          "en",
		ProposedBy:        "sales-agent",
		Title:             "Release checklist",
		Body:              "Peer clearance is required before a production release.",
		Evidence: []knowledge.Evidence{{
			Source: "runbook", Reference: "operations/release.md",
		}},
	})
	if err != nil {
		t.Fatalf("create proposal: %v", err)
	}
	result, err := store.ReviewProposal(ctx, proposal.ID, knowledge.Review{
		Decision: "accept", ReviewedBy: "accounting-owner", Reason: "Rule verified",
	})
	if err != nil {
		t.Fatalf("review proposal: %v", err)
	}
	if result.Revision == nil || result.Revision.RevisionNumber != 1 {
		t.Fatalf("published revision = %#v", result.Revision)
	}
	store.Close()

	store, err = Open(ctx, path)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer store.Close()
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("repeat migrations: %v", err)
	}

	revision, err := store.GetPublished(ctx, "operations/release-check", "en")
	if err != nil {
		t.Fatalf("get published revision: %v", err)
	}
	if revision.Citation == "" || len(revision.Evidence) != 1 {
		t.Fatalf("revision provenance = %#v, citation = %q", revision.Evidence, revision.Citation)
	}
	results, err := store.Search(ctx, "peer clearance", "en", 10)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) != 1 || results[0].RevisionID != revision.ID {
		t.Fatalf("search results = %#v", results)
	}
}

func TestSourceVisibilityPreventsSharedAgentsFromReadingRestrictedWikiPages(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "knowledge.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate store: %v", err)
	}
	service := knowledge.NewService(store)

	shared, err := service.ImportSource(ctx, knowledge.SourceDraft{
		KnowledgeKey: "training/processes/jet", CanonicalLanguage: "en", Language: "en",
		Title: "Jet dyeing", Body: "## Temperature\nPolyester requires 130 degrees.",
		Visibility: knowledge.VisibilityShared, SourceKey: "dh-training",
		SourcePath: "content/en/processes/jet.md", SourceRevision: "abc123",
		SourceURL: "https://git.example/training/abc123/jet", ImportedBy: "admin-agent",
	})
	if err != nil || !shared.Changed {
		t.Fatalf("import shared: result=%#v error=%v", shared, err)
	}
	_, err = service.ImportSource(ctx, knowledge.SourceDraft{
		KnowledgeKey: "training/processes/jet", CanonicalLanguage: "en", Language: "vi",
		Title: "Nhuộm Jet", Body: "## Nhiệt độ\nPolyester cần 130 độ.",
		Visibility: knowledge.VisibilityShared, SourceKey: "dh-training",
		SourcePath: "content/vi/processes/jet.md", SourceRevision: "abc123",
		SourceURL: "https://git.example/training/abc123/vi/jet", ImportedBy: "admin-agent",
	})
	if err != nil {
		t.Fatalf("import translation: %v", err)
	}
	english, err := service.GetPublishedVisible(ctx, "training/processes/jet", "en", false)
	if err != nil || english.SourcePath != "content/en/processes/jet.md" {
		t.Fatalf("English provenance overwritten by translation: %#v error=%v", english, err)
	}
	vietnamese, err := service.GetPublishedVisible(ctx, "training/processes/jet", "vi", false)
	if err != nil || vietnamese.SourcePath != "content/vi/processes/jet.md" {
		t.Fatalf("Vietnamese provenance = %#v error=%v", vietnamese, err)
	}
	restricted, err := service.ImportSource(ctx, knowledge.SourceDraft{
		KnowledgeKey: "handbook/security-recovery", CanonicalLanguage: "en", Language: "en",
		Title: "Security recovery", Body: "## Recovery\nUse the restricted recovery procedure.",
		Visibility: knowledge.VisibilityRestricted, SourceKey: "company-handbook",
		SourcePath: "handbook/security/recovery.md", SourceRevision: "def456",
		SourceURL: "https://git.example/handbook/def456/security-recovery", ImportedBy: "admin-agent",
	})
	if err != nil || !restricted.Changed {
		t.Fatalf("import restricted: result=%#v error=%v", restricted, err)
	}

	sharedResults, err := service.SearchVisible(ctx, "restricted recovery", "en", 10, false)
	if err != nil || len(sharedResults) != 0 {
		t.Fatalf("shared search leaked restricted result: %#v error=%v", sharedResults, err)
	}
	restrictedResults, err := service.SearchVisible(ctx, "restricted recovery", "en", 10, true)
	if err != nil || len(restrictedResults) != 1 || restrictedResults[0].Heading != "Recovery" {
		t.Fatalf("restricted search = %#v error=%v", restrictedResults, err)
	}

	again, err := service.ImportSource(ctx, knowledge.SourceDraft{
		KnowledgeKey: "training/processes/jet", CanonicalLanguage: "en", Language: "en",
		Title: "Jet dyeing", Body: "## Temperature\nPolyester requires 130 degrees.",
		Visibility: knowledge.VisibilityShared, SourceKey: "dh-training",
		SourcePath: "content/en/processes/jet.md", SourceRevision: "abc123",
		SourceURL: "https://git.example/training/abc123/jet", ImportedBy: "admin-agent",
	})
	if err != nil || again.Changed || again.Revision.RevisionNumber != 1 {
		t.Fatalf("idempotent import = %#v error=%v", again, err)
	}
}
