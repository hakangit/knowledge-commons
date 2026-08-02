package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/hakangit/knowledge-commons/internal/knowledge"
)

func (store *Store) CreateProposal(ctx context.Context, draft knowledge.ProposalDraft) (knowledge.Proposal, error) {
	evidence, err := json.Marshal(draft.Evidence)
	if err != nil {
		return knowledge.Proposal{}, err
	}
	transaction, err := store.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return knowledge.Proposal{}, err
	}
	defer transaction.Rollback(context.Background())

	var proposal knowledge.Proposal
	err = transaction.QueryRow(ctx, `
		INSERT INTO knowledge_proposals (
			knowledge_key, canonical_language, base_revision_id, language,
			proposed_by, title, body, evidence
		) VALUES ($1, $2, NULLIF($3, '')::uuid, $4, $5, $6, $7, $8)
		RETURNING id::text, knowledge_key, status, proposed_by`,
		draft.KnowledgeKey, draft.CanonicalLanguage, draft.BaseRevisionID, draft.Language,
		draft.ProposedBy, draft.Title, draft.Body, evidence,
	).Scan(&proposal.ID, &proposal.KnowledgeKey, &proposal.Status, &proposal.ProposedBy)
	if err != nil {
		return knowledge.Proposal{}, err
	}

	_, err = transaction.Exec(ctx, `
		INSERT INTO knowledge_events (event_type, actor_key, subject_type, subject_id, data)
		VALUES ('proposal.created', $1, 'proposal', $2::uuid, jsonb_build_object('knowledge_key', $3::text))`,
		draft.ProposedBy, proposal.ID, draft.KnowledgeKey,
	)
	if err != nil {
		return knowledge.Proposal{}, err
	}
	return proposal, transaction.Commit(ctx)
}

func (store *Store) ReviewProposal(ctx context.Context, proposalID string, review knowledge.Review) (knowledge.ReviewResult, error) {
	transaction, err := store.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return knowledge.ReviewResult{}, err
	}
	defer transaction.Rollback(context.Background())

	proposal, evidenceJSON, err := lockProposal(ctx, transaction, proposalID)
	if err != nil {
		return knowledge.ReviewResult{}, err
	}
	if proposal.Status != "pending" {
		return knowledge.ReviewResult{}, fmt.Errorf("%w: proposal has already been reviewed", knowledge.ErrConflict)
	}
	if proposal.ProposedBy == review.ReviewedBy {
		return knowledge.ReviewResult{}, fmt.Errorf("%w: proposers cannot review their own knowledge", knowledge.ErrConflict)
	}

	if review.Decision == "reject" {
		if _, err := transaction.Exec(ctx, `
			UPDATE knowledge_proposals
			SET status = 'rejected', reviewed_by = $2, reviewed_at = now(), decision_reason = $3
			WHERE id = $1::uuid`, proposalID, review.ReviewedBy, review.Reason); err != nil {
			return knowledge.ReviewResult{}, err
		}
		if err := recordEvent(ctx, transaction, "proposal.rejected", review.ReviewedBy, "proposal", proposalID, review.Reason); err != nil {
			return knowledge.ReviewResult{}, err
		}
		if err := transaction.Commit(ctx); err != nil {
			return knowledge.ReviewResult{}, err
		}
		return knowledge.ReviewResult{ProposalID: proposalID, Status: "rejected"}, nil
	}

	revision, err := publishProposal(ctx, transaction, proposalID, proposal, evidenceJSON, review)
	if err != nil {
		return knowledge.ReviewResult{}, err
	}
	if err := transaction.Commit(ctx); err != nil {
		return knowledge.ReviewResult{}, err
	}
	return knowledge.ReviewResult{ProposalID: proposalID, Status: "accepted", Revision: &revision}, nil
}

type storedProposal struct {
	Status            string
	KnowledgeKey      string
	CanonicalLanguage string
	Language          string
	ProposedBy        string
	Title             string
	Body              string
	BaseRevisionID    string
}

func lockProposal(ctx context.Context, transaction pgx.Tx, proposalID string) (storedProposal, []byte, error) {
	var proposal storedProposal
	var evidence []byte
	err := transaction.QueryRow(ctx, `
		SELECT status, knowledge_key, canonical_language, language, proposed_by,
		       title, body, COALESCE(base_revision_id::text, ''), evidence
		FROM knowledge_proposals
		WHERE id = $1::uuid
		FOR UPDATE`, proposalID,
	).Scan(
		&proposal.Status, &proposal.KnowledgeKey, &proposal.CanonicalLanguage, &proposal.Language,
		&proposal.ProposedBy, &proposal.Title, &proposal.Body, &proposal.BaseRevisionID, &evidence,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return storedProposal{}, nil, knowledge.ErrNotFound
	}
	return proposal, evidence, err
}

func publishProposal(
	ctx context.Context,
	transaction pgx.Tx,
	proposalID string,
	proposal storedProposal,
	evidenceJSON []byte,
	review knowledge.Review,
) (knowledge.Revision, error) {
	if _, err := transaction.Exec(ctx, `
		INSERT INTO knowledge_pages (knowledge_key, canonical_language)
		VALUES ($1, $2)
		ON CONFLICT (knowledge_key) DO NOTHING`, proposal.KnowledgeKey, proposal.CanonicalLanguage); err != nil {
		return knowledge.Revision{}, err
	}

	var pageID, canonicalLanguage string
	if err := transaction.QueryRow(ctx, `
		SELECT id::text, canonical_language
		FROM knowledge_pages
		WHERE knowledge_key = $1
		FOR UPDATE`, proposal.KnowledgeKey).Scan(&pageID, &canonicalLanguage); err != nil {
		return knowledge.Revision{}, err
	}
	if canonicalLanguage != proposal.CanonicalLanguage {
		return knowledge.Revision{}, fmt.Errorf("%w: canonical language is %s", knowledge.ErrConflict, canonicalLanguage)
	}

	var latestRevisionID string
	var latestRevisionNumber int64
	err := transaction.QueryRow(ctx, `
		SELECT id::text, revision_number
		FROM knowledge_revisions
		WHERE page_id = $1::uuid AND language = $2 AND status = 'published'
		ORDER BY revision_number DESC
		LIMIT 1`, pageID, proposal.Language).Scan(&latestRevisionID, &latestRevisionNumber)
	hasPublishedRevision := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return knowledge.Revision{}, err
	}
	if proposal.BaseRevisionID != "" && proposal.BaseRevisionID != latestRevisionID {
		return knowledge.Revision{}, fmt.Errorf("%w: base revision is no longer current", knowledge.ErrConflict)
	}
	if proposal.BaseRevisionID == "" && hasPublishedRevision {
		return knowledge.Revision{}, fmt.Errorf("%w: updates require the current base_revision_id", knowledge.ErrConflict)
	}

	if hasPublishedRevision {
		if _, err := transaction.Exec(ctx, `UPDATE knowledge_revisions SET status = 'superseded' WHERE id = $1::uuid`, latestRevisionID); err != nil {
			return knowledge.Revision{}, err
		}
	}

	var evidence []knowledge.Evidence
	if err := json.Unmarshal(evidenceJSON, &evidence); err != nil {
		return knowledge.Revision{}, err
	}
	provenance, err := json.Marshal(map[string]any{
		"proposal_id": proposalID,
		"evidence":    evidence,
		"review": map[string]string{
			"reviewed_by": review.ReviewedBy,
			"reason":      review.Reason,
		},
	})
	if err != nil {
		return knowledge.Revision{}, err
	}

	revision := knowledge.Revision{
		PageID:         pageID,
		KnowledgeKey:   proposal.KnowledgeKey,
		RevisionNumber: latestRevisionNumber + 1,
		Language:       proposal.Language,
		Title:          proposal.Title,
		Body:           proposal.Body,
		AuthoredBy:     proposal.ProposedBy,
		Evidence:       evidence,
	}
	if err := transaction.QueryRow(ctx, `
		INSERT INTO knowledge_revisions (
			page_id, revision_number, language, title, body, status, authored_by, provenance
		) VALUES ($1::uuid, $2, $3, $4, $5, 'published', $6, $7)
		RETURNING id::text`,
		pageID, revision.RevisionNumber, revision.Language, revision.Title, revision.Body,
		revision.AuthoredBy, provenance,
	).Scan(&revision.ID); err != nil {
		return knowledge.Revision{}, err
	}
	revision.Citation = knowledge.Citation(revision.KnowledgeKey, revision.Language, revision.RevisionNumber)

	if _, err := transaction.Exec(ctx, `
		UPDATE knowledge_proposals
		SET page_id = $2::uuid, status = 'accepted', reviewed_by = $3,
		    reviewed_at = now(), decision_reason = $4
		WHERE id = $1::uuid`, proposalID, pageID, review.ReviewedBy, review.Reason); err != nil {
		return knowledge.Revision{}, err
	}
	if _, err := transaction.Exec(ctx, `
		UPDATE knowledge_pages SET status = 'published', updated_at = now() WHERE id = $1::uuid`, pageID); err != nil {
		return knowledge.Revision{}, err
	}
	if err := recordEvent(ctx, transaction, "proposal.accepted", review.ReviewedBy, "proposal", proposalID, review.Reason); err != nil {
		return knowledge.Revision{}, err
	}
	if err := recordEvent(ctx, transaction, "revision.published", review.ReviewedBy, "revision", revision.ID, revision.Citation); err != nil {
		return knowledge.Revision{}, err
	}
	return revision, nil
}

func recordEvent(ctx context.Context, transaction pgx.Tx, eventType, actor, subjectType, subjectID, detail string) error {
	_, err := transaction.Exec(ctx, `
		INSERT INTO knowledge_events (event_type, actor_key, subject_type, subject_id, data)
		VALUES ($1, $2, $3, $4::uuid, jsonb_build_object('detail', $5::text))`,
		eventType, actor, subjectType, subjectID, detail,
	)
	return err
}

func (store *Store) GetPublished(ctx context.Context, knowledgeKey, language string) (knowledge.Revision, error) {
	var revision knowledge.Revision
	var evidenceJSON []byte
	err := store.pool.QueryRow(ctx, `
		SELECT revision.id::text, revision.page_id::text, page.knowledge_key,
		       revision.revision_number, revision.language, revision.title, revision.body,
		       revision.authored_by, COALESCE(revision.provenance->'evidence', '[]'::jsonb)
		FROM knowledge_revisions AS revision
		JOIN knowledge_pages AS page ON page.id = revision.page_id
		WHERE page.knowledge_key = $1 AND revision.language = $2 AND revision.status = 'published'
		ORDER BY revision.revision_number DESC
		LIMIT 1`, knowledgeKey, language,
	).Scan(
		&revision.ID, &revision.PageID, &revision.KnowledgeKey, &revision.RevisionNumber,
		&revision.Language, &revision.Title, &revision.Body, &revision.AuthoredBy, &evidenceJSON,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return knowledge.Revision{}, knowledge.ErrNotFound
	}
	if err != nil {
		return knowledge.Revision{}, err
	}
	if err := json.Unmarshal(evidenceJSON, &revision.Evidence); err != nil {
		return knowledge.Revision{}, err
	}
	revision.Citation = knowledge.Citation(revision.KnowledgeKey, revision.Language, revision.RevisionNumber)
	return revision, nil
}

func (store *Store) Search(ctx context.Context, query, language string, limit int) ([]knowledge.SearchResult, error) {
	rows, err := store.pool.Query(ctx, `
		WITH input AS (
			SELECT websearch_to_tsquery('simple', $1) AS terms, lower($1) AS text
		)
		SELECT page.knowledge_key, revision.id::text, revision.revision_number,
		       revision.language, revision.title,
		       ts_headline('simple', revision.body, input.terms, 'MaxFragments=2, MaxWords=24, MinWords=8'),
		       GREATEST(
		           ts_rank_cd(revision.search_document, input.terms),
		           similarity(lower(revision.title), input.text) * 0.8,
		           similarity(lower(revision.body), input.text) * 0.2,
		           CASE WHEN lower(revision.body) LIKE '%' || input.text || '%' THEN 0.3 ELSE 0 END
		       )::real AS score
		FROM knowledge_revisions AS revision
		JOIN knowledge_pages AS page ON page.id = revision.page_id
		CROSS JOIN input
		WHERE revision.status = 'published'
		  AND ($2 = '' OR revision.language = $2)
		  AND (
		      revision.search_document @@ input.terms
		      OR lower(revision.title) LIKE '%' || input.text || '%'
		      OR lower(revision.body) LIKE '%' || input.text || '%'
		  )
		ORDER BY score DESC, revision.created_at DESC
		LIMIT $3`, query, language, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	results := make([]knowledge.SearchResult, 0)
	for rows.Next() {
		var result knowledge.SearchResult
		if err := rows.Scan(
			&result.KnowledgeKey, &result.RevisionID, &result.RevisionNumber,
			&result.Language, &result.Title, &result.Excerpt, &result.Score,
		); err != nil {
			return nil, err
		}
		result.Citation = knowledge.Citation(result.KnowledgeKey, result.Language, result.RevisionNumber)
		results = append(results, result)
	}
	return results, rows.Err()
}
