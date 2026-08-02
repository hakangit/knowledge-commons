package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/hakangit/knowledge-commons/internal/identifier"
	"github.com/hakangit/knowledge-commons/internal/knowledge"
)

func (store *Store) UpsertSource(ctx context.Context, draft knowledge.SourceDraft) (knowledge.SourceResult, error) {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return knowledge.SourceResult{}, err
	}
	defer tx.Rollback()

	now := timestamp()
	pageID := identifier.New()
	_, err = tx.ExecContext(ctx, `
		INSERT INTO knowledge_pages (
			id, knowledge_key, canonical_language, status, kind, visibility,
			source_key, source_path, created_at, updated_at
		) VALUES (?, ?, ?, 'published', 'source', ?, ?, ?, ?, ?)
		ON CONFLICT (knowledge_key) DO NOTHING`,
		pageID, draft.KnowledgeKey, draft.CanonicalLanguage, draft.Visibility,
		draft.SourceKey, draft.SourcePath, now, now,
	)
	if err != nil {
		return knowledge.SourceResult{}, err
	}

	var canonicalLanguage, kind string
	if err := tx.QueryRowContext(ctx, `
		SELECT id, canonical_language, kind
		FROM knowledge_pages WHERE knowledge_key = ?`, draft.KnowledgeKey,
	).Scan(&pageID, &canonicalLanguage, &kind); err != nil {
		return knowledge.SourceResult{}, err
	}
	if canonicalLanguage != draft.CanonicalLanguage || kind != "source" {
		return knowledge.SourceResult{}, fmt.Errorf("%w: knowledge key belongs to another canonical source", knowledge.ErrConflict)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE knowledge_pages
		SET visibility = ?, source_key = ?, source_path = ?, updated_at = ?
		WHERE id = ?`, draft.Visibility, draft.SourceKey, draft.SourcePath, now, pageID,
	); err != nil {
		return knowledge.SourceResult{}, err
	}

	current, hasCurrent, err := currentSourceRevision(ctx, tx, pageID, draft.Language)
	if err != nil {
		return knowledge.SourceResult{}, err
	}
	if hasCurrent && current.ContentHash == draft.ContentHash {
		if _, err := tx.ExecContext(ctx, `
			UPDATE knowledge_revisions
			SET source_revision = ?, source_url = ?, source_path = ?
			WHERE id = ?`, draft.SourceRevision, draft.SourceURL, draft.SourcePath, current.ID,
		); err != nil {
			return knowledge.SourceResult{}, err
		}
		current.SourceRevision = draft.SourceRevision
		current.SourceURL = draft.SourceURL
		current.SourcePath = draft.SourcePath
		current.Citation = draft.SourceURL
		if err := tx.Commit(); err != nil {
			return knowledge.SourceResult{}, err
		}
		return knowledge.SourceResult{Changed: false, Revision: current}, nil
	}
	if hasCurrent {
		if _, err := tx.ExecContext(ctx,
			"UPDATE knowledge_revisions SET status = 'superseded' WHERE id = ?", current.ID,
		); err != nil {
			return knowledge.SourceResult{}, err
		}
	}

	evidence := []knowledge.Evidence{{
		Source: "git", Reference: draft.SourceURL,
		Summary: "Imported from " + draft.SourceKey + " at " + draft.SourceRevision,
	}}
	encodedEvidence, err := json.Marshal(evidence)
	if err != nil {
		return knowledge.SourceResult{}, err
	}
	revision := knowledge.Revision{
		ID: identifier.New(), PageID: pageID, KnowledgeKey: draft.KnowledgeKey,
		RevisionNumber: current.RevisionNumber + 1, Language: draft.Language,
		Title: draft.Title, Body: draft.Body, AuthoredBy: draft.ImportedBy,
		Evidence: evidence, Citation: draft.SourceURL, Kind: "source",
		Visibility: draft.Visibility, SourceKey: draft.SourceKey,
		SourcePath: draft.SourcePath, SourceRevision: draft.SourceRevision,
		SourceURL: draft.SourceURL, ContentHash: draft.ContentHash,
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO knowledge_revisions (
			id, page_id, revision_number, language, title, body, status,
			authored_by, evidence, source_revision, source_url, source_path, content_hash, created_at
		) VALUES (?, ?, ?, ?, ?, ?, 'published', ?, ?, ?, ?, ?, ?, ?)`,
		revision.ID, pageID, revision.RevisionNumber, revision.Language,
		revision.Title, revision.Body, revision.AuthoredBy, encodedEvidence,
		revision.SourceRevision, revision.SourceURL, revision.SourcePath, revision.ContentHash, now,
	)
	if err != nil {
		return knowledge.SourceResult{}, err
	}
	if _, err := tx.ExecContext(ctx,
		"INSERT INTO knowledge_revisions_fts (revision_id, title, body) VALUES (?, ?, ?)",
		revision.ID, revision.Title, revision.Body,
	); err != nil {
		return knowledge.SourceResult{}, err
	}
	if err := insertChunks(ctx, tx, revision.ID, revision.Title, revision.Body); err != nil {
		return knowledge.SourceResult{}, err
	}
	if err := recordEvent(ctx, tx, "source.synced", draft.ImportedBy, "revision", revision.ID,
		map[string]string{"knowledge_key": draft.KnowledgeKey, "source_revision": draft.SourceRevision}); err != nil {
		return knowledge.SourceResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return knowledge.SourceResult{}, err
	}
	return knowledge.SourceResult{Changed: true, Revision: revision}, nil
}

func currentSourceRevision(ctx context.Context, tx *sql.Tx, pageID, language string) (knowledge.Revision, bool, error) {
	var revision knowledge.Revision
	var evidence []byte
	err := tx.QueryRowContext(ctx, `
		SELECT revision.id, revision.page_id, page.knowledge_key,
		       revision.revision_number, revision.language, revision.title,
		       revision.body, revision.authored_by, revision.evidence,
		       page.kind, page.visibility, COALESCE(page.source_key, ''),
		       COALESCE(revision.source_path, ''), COALESCE(revision.source_revision, ''),
		       COALESCE(revision.source_url, ''), COALESCE(revision.content_hash, '')
		FROM knowledge_revisions AS revision
		JOIN knowledge_pages AS page ON page.id = revision.page_id
		WHERE revision.page_id = ? AND revision.language = ? AND revision.status = 'published'
		ORDER BY revision.revision_number DESC LIMIT 1`, pageID, language,
	).Scan(
		&revision.ID, &revision.PageID, &revision.KnowledgeKey,
		&revision.RevisionNumber, &revision.Language, &revision.Title,
		&revision.Body, &revision.AuthoredBy, &evidence,
		&revision.Kind, &revision.Visibility, &revision.SourceKey,
		&revision.SourcePath, &revision.SourceRevision, &revision.SourceURL,
		&revision.ContentHash,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return knowledge.Revision{}, false, nil
	}
	if err != nil {
		return knowledge.Revision{}, false, err
	}
	if err := json.Unmarshal(evidence, &revision.Evidence); err != nil {
		return knowledge.Revision{}, false, err
	}
	revision.Citation = revision.SourceURL
	return revision, true, nil
}
