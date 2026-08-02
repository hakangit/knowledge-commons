package sqlite

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	_ "modernc.org/sqlite"

	"github.com/hakangit/knowledge-commons/internal/identifier"
	"github.com/hakangit/knowledge-commons/internal/knowledge"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

type Store struct {
	db *sql.DB
}

func Open(ctx context.Context, path string) (*Store, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("sqlite path is required")
	}

	dsn, err := sqliteDSN(path)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	store := &Store{db: db}
	if err := store.Ping(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return store, nil
}

func sqliteDSN(path string) (string, error) {
	if path == ":memory:" || strings.HasPrefix(path, "file:") {
		separator := "?"
		if strings.Contains(path, "?") {
			separator = "&"
		}
		return path + separator + "_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)", nil
	}

	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(absolutePath), 0o750); err != nil {
		return "", err
	}
	dsn := &url.URL{Scheme: "file", Path: absolutePath}
	query := dsn.Query()
	query.Add("_pragma", "foreign_keys(1)")
	query.Add("_pragma", "busy_timeout(5000)")
	query.Add("_pragma", "journal_mode(WAL)")
	dsn.RawQuery = query.Encode()
	return dsn.String(), nil
}

func (store *Store) Ping(ctx context.Context) error {
	return store.db.PingContext(ctx)
}

func (store *Store) Close() {
	_ = store.db.Close()
}

func (store *Store) Migrate(ctx context.Context) error {
	if _, err := store.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			name TEXT PRIMARY KEY,
			applied_at TEXT NOT NULL
		)`); err != nil {
		return err
	}

	entries, err := fs.ReadDir(migrationFiles, "migrations")
	if err != nil {
		return err
	}
	sort.Slice(entries, func(left, right int) bool { return entries[left].Name() < entries[right].Name() })

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if err := store.applyMigration(ctx, entry.Name()); err != nil {
			return err
		}
	}
	return nil
}

func (store *Store) applyMigration(ctx context.Context, name string) error {
	var applied bool
	if err := store.db.QueryRowContext(ctx,
		"SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE name = ?)", name,
	).Scan(&applied); err != nil {
		return err
	}
	if applied {
		return nil
	}

	contents, err := migrationFiles.ReadFile("migrations/" + name)
	if err != nil {
		return err
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, string(contents)); err != nil {
		return fmt.Errorf("apply %s: %w", name, err)
	}
	if _, err := tx.ExecContext(ctx,
		"INSERT INTO schema_migrations (name, applied_at) VALUES (?, ?)", name, timestamp(),
	); err != nil {
		return err
	}
	return tx.Commit()
}

func (store *Store) CreateProposal(ctx context.Context, draft knowledge.ProposalDraft) (knowledge.Proposal, error) {
	evidence, err := json.Marshal(draft.Evidence)
	if err != nil {
		return knowledge.Proposal{}, err
	}
	proposal := knowledge.Proposal{
		ID:           identifier.New(),
		KnowledgeKey: draft.KnowledgeKey,
		Status:       "pending",
		ProposedBy:   draft.ProposedBy,
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return knowledge.Proposal{}, err
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx, `
		INSERT INTO knowledge_proposals (
			id, knowledge_key, canonical_language, base_revision_id, language,
			proposed_by, title, body, evidence, status, created_at
		) VALUES (?, ?, ?, NULLIF(?, ''), ?, ?, ?, ?, ?, 'pending', ?)`,
		proposal.ID, draft.KnowledgeKey, draft.CanonicalLanguage, draft.BaseRevisionID,
		draft.Language, draft.ProposedBy, draft.Title, draft.Body, evidence, timestamp(),
	)
	if err != nil {
		return knowledge.Proposal{}, err
	}
	if err := recordEvent(ctx, tx, "proposal.created", draft.ProposedBy, "proposal", proposal.ID,
		map[string]string{"knowledge_key": draft.KnowledgeKey}); err != nil {
		return knowledge.Proposal{}, err
	}
	if err := tx.Commit(); err != nil {
		return knowledge.Proposal{}, err
	}
	return proposal, nil
}

type storedProposal struct {
	Status            string
	KnowledgeKey      string
	CanonicalLanguage string
	Language          string
	ProposedBy        string
	Title             string
	Body              string
	BaseRevisionID    sql.NullString
	Evidence          []byte
}

func (store *Store) ReviewProposal(ctx context.Context, proposalID string, review knowledge.Review) (knowledge.ReviewResult, error) {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return knowledge.ReviewResult{}, err
	}
	defer tx.Rollback()

	var proposal storedProposal
	err = tx.QueryRowContext(ctx, `
		SELECT status, knowledge_key, canonical_language, language, proposed_by,
		       title, body, base_revision_id, evidence
		FROM knowledge_proposals WHERE id = ?`, proposalID,
	).Scan(
		&proposal.Status, &proposal.KnowledgeKey, &proposal.CanonicalLanguage,
		&proposal.Language, &proposal.ProposedBy, &proposal.Title, &proposal.Body,
		&proposal.BaseRevisionID, &proposal.Evidence,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return knowledge.ReviewResult{}, knowledge.ErrNotFound
	}
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
		if _, err := tx.ExecContext(ctx, `
			UPDATE knowledge_proposals
			SET status = 'rejected', reviewed_by = ?, reviewed_at = ?, decision_reason = ?
			WHERE id = ?`, review.ReviewedBy, timestamp(), review.Reason, proposalID); err != nil {
			return knowledge.ReviewResult{}, err
		}
		if err := recordEvent(ctx, tx, "proposal.rejected", review.ReviewedBy, "proposal", proposalID,
			map[string]string{"reason": review.Reason}); err != nil {
			return knowledge.ReviewResult{}, err
		}
		if err := tx.Commit(); err != nil {
			return knowledge.ReviewResult{}, err
		}
		return knowledge.ReviewResult{ProposalID: proposalID, Status: "rejected"}, nil
	}

	revision, err := publishProposal(ctx, tx, proposalID, proposal, review)
	if err != nil {
		return knowledge.ReviewResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return knowledge.ReviewResult{}, err
	}
	return knowledge.ReviewResult{ProposalID: proposalID, Status: "accepted", Revision: &revision}, nil
}

func publishProposal(
	ctx context.Context,
	tx *sql.Tx,
	proposalID string,
	proposal storedProposal,
	review knowledge.Review,
) (knowledge.Revision, error) {
	now := timestamp()
	pageID := identifier.New()
	_, err := tx.ExecContext(ctx, `
		INSERT INTO knowledge_pages (id, knowledge_key, canonical_language, status, created_at, updated_at)
		VALUES (?, ?, ?, 'draft', ?, ?)
		ON CONFLICT (knowledge_key) DO NOTHING`,
		pageID, proposal.KnowledgeKey, proposal.CanonicalLanguage, now, now,
	)
	if err != nil {
		return knowledge.Revision{}, err
	}

	var canonicalLanguage, kind string
	if err := tx.QueryRowContext(ctx,
		"SELECT id, canonical_language, kind FROM knowledge_pages WHERE knowledge_key = ?",
		proposal.KnowledgeKey,
	).Scan(&pageID, &canonicalLanguage, &kind); err != nil {
		return knowledge.Revision{}, err
	}
	if kind == "source" {
		return knowledge.Revision{}, fmt.Errorf("%w: source-backed knowledge must be changed in its canonical repository", knowledge.ErrConflict)
	}
	if canonicalLanguage != proposal.CanonicalLanguage {
		return knowledge.Revision{}, fmt.Errorf("%w: canonical language is %s", knowledge.ErrConflict, canonicalLanguage)
	}

	var latestRevisionID string
	var latestRevisionNumber int64
	err = tx.QueryRowContext(ctx, `
		SELECT id, revision_number
		FROM knowledge_revisions
		WHERE page_id = ? AND language = ? AND status = 'published'
		ORDER BY revision_number DESC LIMIT 1`, pageID, proposal.Language,
	).Scan(&latestRevisionID, &latestRevisionNumber)
	hasPublishedRevision := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return knowledge.Revision{}, err
	}
	if proposal.BaseRevisionID.Valid && proposal.BaseRevisionID.String != latestRevisionID {
		return knowledge.Revision{}, fmt.Errorf("%w: base revision is no longer current", knowledge.ErrConflict)
	}
	if !proposal.BaseRevisionID.Valid && hasPublishedRevision {
		return knowledge.Revision{}, fmt.Errorf("%w: updates require the current base_revision_id", knowledge.ErrConflict)
	}
	if hasPublishedRevision {
		if _, err := tx.ExecContext(ctx,
			"UPDATE knowledge_revisions SET status = 'superseded' WHERE id = ?", latestRevisionID,
		); err != nil {
			return knowledge.Revision{}, err
		}
	}

	var evidence []knowledge.Evidence
	if err := json.Unmarshal(proposal.Evidence, &evidence); err != nil {
		return knowledge.Revision{}, err
	}
	revision := knowledge.Revision{
		ID:             identifier.New(),
		PageID:         pageID,
		KnowledgeKey:   proposal.KnowledgeKey,
		RevisionNumber: latestRevisionNumber + 1,
		Language:       proposal.Language,
		Title:          proposal.Title,
		Body:           proposal.Body,
		AuthoredBy:     proposal.ProposedBy,
		Evidence:       evidence,
	}
	revision.Citation = knowledge.Citation(revision.KnowledgeKey, revision.Language, revision.RevisionNumber)

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO knowledge_revisions (
			id, page_id, revision_number, language, title, body, status, authored_by, evidence, created_at
		) VALUES (?, ?, ?, ?, ?, ?, 'published', ?, ?, ?)`,
		revision.ID, pageID, revision.RevisionNumber, revision.Language, revision.Title,
		revision.Body, revision.AuthoredBy, proposal.Evidence, now,
	); err != nil {
		return knowledge.Revision{}, err
	}
	if _, err := tx.ExecContext(ctx,
		"INSERT INTO knowledge_revisions_fts (revision_id, title, body) VALUES (?, ?, ?)",
		revision.ID, revision.Title, revision.Body,
	); err != nil {
		return knowledge.Revision{}, err
	}
	if err := insertChunks(ctx, tx, revision.ID, revision.Title, revision.Body); err != nil {
		return knowledge.Revision{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE knowledge_proposals
		SET page_id = ?, status = 'accepted', reviewed_by = ?, reviewed_at = ?, decision_reason = ?
		WHERE id = ?`, pageID, review.ReviewedBy, now, review.Reason, proposalID,
	); err != nil {
		return knowledge.Revision{}, err
	}
	if _, err := tx.ExecContext(ctx,
		"UPDATE knowledge_pages SET status = 'published', updated_at = ? WHERE id = ?", now, pageID,
	); err != nil {
		return knowledge.Revision{}, err
	}
	if err := recordEvent(ctx, tx, "proposal.accepted", review.ReviewedBy, "proposal", proposalID,
		map[string]string{"reason": review.Reason}); err != nil {
		return knowledge.Revision{}, err
	}
	if err := recordEvent(ctx, tx, "revision.published", review.ReviewedBy, "revision", revision.ID,
		map[string]string{"citation": revision.Citation}); err != nil {
		return knowledge.Revision{}, err
	}
	return revision, nil
}

func (store *Store) GetPublished(ctx context.Context, knowledgeKey, language string) (knowledge.Revision, error) {
	return store.GetPublishedVisible(ctx, knowledgeKey, language, false)
}

func (store *Store) GetPublishedVisible(ctx context.Context, knowledgeKey, language string, includeRestricted bool) (knowledge.Revision, error) {
	var revision knowledge.Revision
	var evidence []byte
	err := store.db.QueryRowContext(ctx, `
		SELECT revision.id, revision.page_id, page.knowledge_key,
		       revision.revision_number, revision.language, revision.title, revision.body,
		       revision.authored_by, revision.evidence, page.kind, page.visibility,
		       COALESCE(page.source_key, ''), COALESCE(revision.source_path, ''),
		       COALESCE(revision.source_revision, ''), COALESCE(revision.source_url, ''),
		       COALESCE(revision.content_hash, '')
		FROM knowledge_revisions AS revision
		JOIN knowledge_pages AS page ON page.id = revision.page_id
		WHERE page.knowledge_key = ? AND revision.language = ? AND revision.status = 'published'
		  AND (? OR page.visibility = 'shared')
		ORDER BY revision.revision_number DESC LIMIT 1`, knowledgeKey, language,
		includeRestricted,
	).Scan(
		&revision.ID, &revision.PageID, &revision.KnowledgeKey, &revision.RevisionNumber,
		&revision.Language, &revision.Title, &revision.Body, &revision.AuthoredBy, &evidence,
		&revision.Kind, &revision.Visibility, &revision.SourceKey, &revision.SourcePath,
		&revision.SourceRevision, &revision.SourceURL, &revision.ContentHash,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return knowledge.Revision{}, knowledge.ErrNotFound
	}
	if err != nil {
		return knowledge.Revision{}, err
	}
	if err := json.Unmarshal(evidence, &revision.Evidence); err != nil {
		return knowledge.Revision{}, err
	}
	revision.Citation = revision.SourceURL
	if revision.Citation == "" {
		revision.Citation = knowledge.Citation(revision.KnowledgeKey, revision.Language, revision.RevisionNumber)
	}
	return revision, nil
}

func (store *Store) Search(ctx context.Context, query, language string, limit int) ([]knowledge.SearchResult, error) {
	return store.SearchVisible(ctx, query, language, limit, false)
}

func (store *Store) SearchVisible(ctx context.Context, query, language string, limit int, includeRestricted bool) ([]knowledge.SearchResult, error) {
	match := ftsQuery(query)
	if match == "" {
		return []knowledge.SearchResult{}, nil
	}
	rows, err := store.db.QueryContext(ctx, `
		SELECT page.knowledge_key, revision.id, revision.revision_number,
		       revision.language, revision.title, chunk.heading,
		       snippet(knowledge_chunks_fts, 3, '', '', ' … ', 24),
		       bm25(knowledge_chunks_fts), page.kind, page.visibility,
		       COALESCE(page.source_key, ''), COALESCE(revision.source_path, ''),
		       COALESCE(revision.source_url, '')
		FROM knowledge_chunks_fts
		JOIN knowledge_chunks AS chunk ON chunk.id = knowledge_chunks_fts.chunk_id
		JOIN knowledge_revisions AS revision ON revision.id = chunk.revision_id
		JOIN knowledge_pages AS page ON page.id = revision.page_id
		WHERE knowledge_chunks_fts MATCH ?
		  AND revision.status = 'published'
		  AND (? = '' OR revision.language = ?)
		  AND (? OR page.visibility = 'shared')
		ORDER BY bm25(knowledge_chunks_fts), revision.revision_number DESC
		LIMIT ?`, match, language, language, includeRestricted, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	results := make([]knowledge.SearchResult, 0)
	for rows.Next() {
		var result knowledge.SearchResult
		var rank float64
		if err := rows.Scan(
			&result.KnowledgeKey, &result.RevisionID, &result.RevisionNumber,
			&result.Language, &result.Title, &result.Heading, &result.Excerpt, &rank,
			&result.Kind, &result.Visibility, &result.SourceKey, &result.SourcePath,
			&result.SourceURL,
		); err != nil {
			return nil, err
		}
		result.Score = float32(1 / (1 + math.Abs(rank)))
		result.Citation = result.SourceURL
		if result.Citation == "" {
			result.Citation = knowledge.Citation(result.KnowledgeKey, result.Language, result.RevisionNumber)
		}
		results = append(results, result)
	}
	return results, rows.Err()
}

func recordEvent(
	ctx context.Context,
	tx *sql.Tx,
	eventType, actor, subjectType, subjectID string,
	data map[string]string,
) error {
	encoded, err := json.Marshal(data)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO knowledge_events (
			event_type, actor_key, subject_type, subject_id, data, created_at
		) VALUES (?, ?, ?, ?, ?, ?)`, eventType, actor, subjectType, subjectID, encoded, timestamp(),
	)
	return err
}

func insertChunks(ctx context.Context, tx *sql.Tx, revisionID, title, body string) error {
	for _, chunk := range knowledge.SplitMarkdown(body) {
		chunkID := identifier.New()
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO knowledge_chunks (id, revision_id, ordinal, heading, body)
			VALUES (?, ?, ?, ?, ?)`, chunkID, revisionID, chunk.Ordinal, chunk.Heading, chunk.Body,
		); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO knowledge_chunks_fts (chunk_id, title, heading, body)
			VALUES (?, ?, ?, ?)`, chunkID, title, chunk.Heading, chunk.Body,
		); err != nil {
			return err
		}
	}
	return nil
}

func ftsQuery(value string) string {
	var terms []string
	var current strings.Builder
	flush := func() {
		if current.Len() == 0 {
			return
		}
		terms = append(terms, `"`+current.String()+`"`)
		current.Reset()
	}
	for _, character := range strings.ToLower(value) {
		if unicode.IsLetter(character) || unicode.IsDigit(character) {
			current.WriteRune(character)
			continue
		}
		flush()
	}
	flush()
	return strings.Join(terms, " AND ")
}

func timestamp() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}
