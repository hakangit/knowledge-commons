package knowledge

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"unicode"
)

var (
	ErrInvalid  = errors.New("invalid knowledge request")
	ErrNotFound = errors.New("knowledge not found")
	ErrConflict = errors.New("knowledge conflict")
)

type Evidence struct {
	Source    string `json:"source"`
	Reference string `json:"reference"`
	Summary   string `json:"summary,omitempty"`
}

type ProposalDraft struct {
	KnowledgeKey      string     `json:"knowledge_key"`
	CanonicalLanguage string     `json:"canonical_language"`
	Language          string     `json:"language"`
	ProposedBy        string     `json:"-"`
	Title             string     `json:"title"`
	Body              string     `json:"body"`
	BaseRevisionID    string     `json:"base_revision_id,omitempty"`
	Evidence          []Evidence `json:"evidence"`
}

type Proposal struct {
	ID           string `json:"id"`
	KnowledgeKey string `json:"knowledge_key"`
	Status       string `json:"status"`
	ProposedBy   string `json:"proposed_by"`
}

type Review struct {
	Decision   string `json:"decision"`
	ReviewedBy string `json:"-"`
	Reason     string `json:"reason"`
}

type Revision struct {
	ID             string     `json:"id"`
	PageID         string     `json:"page_id"`
	KnowledgeKey   string     `json:"knowledge_key"`
	RevisionNumber int64      `json:"revision_number"`
	Language       string     `json:"language"`
	Title          string     `json:"title"`
	Body           string     `json:"body"`
	AuthoredBy     string     `json:"authored_by"`
	Evidence       []Evidence `json:"evidence"`
	Citation       string     `json:"citation"`
	Kind           string     `json:"kind,omitempty"`
	Visibility     string     `json:"visibility,omitempty"`
	SourceKey      string     `json:"source_key,omitempty"`
	SourcePath     string     `json:"source_path,omitempty"`
	SourceRevision string     `json:"source_revision,omitempty"`
	SourceURL      string     `json:"source_url,omitempty"`
	ContentHash    string     `json:"content_hash,omitempty"`
}

type ReviewResult struct {
	ProposalID string    `json:"proposal_id"`
	Status     string    `json:"status"`
	Revision   *Revision `json:"revision,omitempty"`
}

type SearchResult struct {
	KnowledgeKey   string  `json:"knowledge_key"`
	RevisionID     string  `json:"revision_id"`
	RevisionNumber int64   `json:"revision_number"`
	Language       string  `json:"language"`
	Title          string  `json:"title"`
	Excerpt        string  `json:"excerpt"`
	Score          float32 `json:"score"`
	Citation       string  `json:"citation"`
	Kind           string  `json:"kind,omitempty"`
	Visibility     string  `json:"visibility,omitempty"`
	Heading        string  `json:"heading,omitempty"`
	SourceKey      string  `json:"source_key,omitempty"`
	SourcePath     string  `json:"source_path,omitempty"`
	SourceURL      string  `json:"source_url,omitempty"`
}

const (
	VisibilityShared     = "shared"
	VisibilityRestricted = "restricted"
)

type SourceDraft struct {
	KnowledgeKey      string `json:"knowledge_key"`
	CanonicalLanguage string `json:"canonical_language"`
	Language          string `json:"language"`
	Title             string `json:"title"`
	Body              string `json:"body"`
	Visibility        string `json:"visibility"`
	SourceKey         string `json:"source_key"`
	SourcePath        string `json:"source_path"`
	SourceRevision    string `json:"source_revision"`
	SourceURL         string `json:"source_url"`
	ImportedBy        string `json:"-"`
	ContentHash       string `json:"-"`
}

type SourceResult struct {
	Changed  bool     `json:"changed"`
	Revision Revision `json:"revision"`
}

type Repository interface {
	CreateProposal(context.Context, ProposalDraft) (Proposal, error)
	ReviewProposal(context.Context, string, Review) (ReviewResult, error)
	GetPublished(context.Context, string, string) (Revision, error)
	Search(context.Context, string, string, int) ([]SearchResult, error)
}

type VisibleRepository interface {
	GetPublishedVisible(context.Context, string, string, bool) (Revision, error)
	SearchVisible(context.Context, string, string, int, bool) ([]SearchResult, error)
}

type SourceRepository interface {
	UpsertSource(context.Context, SourceDraft) (SourceResult, error)
}

type Service struct {
	repository Repository
}

func NewService(repository Repository) *Service {
	return &Service{repository: repository}
}

func (service *Service) Propose(ctx context.Context, draft ProposalDraft) (Proposal, error) {
	draft = normalizeDraft(draft)
	if err := validateDraft(draft); err != nil {
		return Proposal{}, err
	}
	return service.repository.CreateProposal(ctx, draft)
}

func (service *Service) Review(ctx context.Context, proposalID string, review Review) (ReviewResult, error) {
	proposalID = strings.TrimSpace(proposalID)
	review.Decision = strings.ToLower(strings.TrimSpace(review.Decision))
	review.ReviewedBy = strings.TrimSpace(review.ReviewedBy)
	review.Reason = strings.TrimSpace(review.Reason)

	if !validUUID(proposalID) || review.ReviewedBy == "" {
		return ReviewResult{}, fmt.Errorf("%w: proposal id and reviewer are required", ErrInvalid)
	}
	if review.Decision != "accept" && review.Decision != "reject" {
		return ReviewResult{}, fmt.Errorf("%w: decision must be accept or reject", ErrInvalid)
	}
	if review.Reason == "" {
		return ReviewResult{}, fmt.Errorf("%w: review reason is required", ErrInvalid)
	}
	return service.repository.ReviewProposal(ctx, proposalID, review)
}

func (service *Service) GetPublished(ctx context.Context, knowledgeKey, language string) (Revision, error) {
	return service.GetPublishedVisible(ctx, knowledgeKey, language, false)
}

func (service *Service) GetPublishedVisible(ctx context.Context, knowledgeKey, language string, includeRestricted bool) (Revision, error) {
	knowledgeKey = strings.TrimSpace(knowledgeKey)
	language = strings.TrimSpace(language)
	if knowledgeKey == "" || language == "" {
		return Revision{}, fmt.Errorf("%w: knowledge key and language are required", ErrInvalid)
	}
	if repository, ok := service.repository.(VisibleRepository); ok {
		return repository.GetPublishedVisible(ctx, knowledgeKey, language, includeRestricted)
	}
	return service.repository.GetPublished(ctx, knowledgeKey, language)
}

func (service *Service) Search(ctx context.Context, query, language string, limit int) ([]SearchResult, error) {
	return service.SearchVisible(ctx, query, language, limit, false)
}

func (service *Service) SearchVisible(ctx context.Context, query, language string, limit int, includeRestricted bool) ([]SearchResult, error) {
	query = strings.TrimSpace(query)
	language = strings.TrimSpace(language)
	if len([]rune(query)) < 2 {
		return nil, fmt.Errorf("%w: search query must contain at least two characters", ErrInvalid)
	}
	if limit == 0 {
		limit = 10
	}
	if limit < 1 || limit > 50 {
		return nil, fmt.Errorf("%w: search limit must be between 1 and 50", ErrInvalid)
	}
	if repository, ok := service.repository.(VisibleRepository); ok {
		return repository.SearchVisible(ctx, query, language, limit, includeRestricted)
	}
	return service.repository.Search(ctx, query, language, limit)
}

func (service *Service) ImportSource(ctx context.Context, draft SourceDraft) (SourceResult, error) {
	repository, ok := service.repository.(SourceRepository)
	if !ok {
		return SourceResult{}, fmt.Errorf("source ingestion is not supported by this storage provider")
	}
	draft.KnowledgeKey = strings.ToLower(strings.Trim(strings.TrimSpace(draft.KnowledgeKey), "/"))
	draft.CanonicalLanguage = strings.ToLower(strings.TrimSpace(draft.CanonicalLanguage))
	draft.Language = strings.ToLower(strings.TrimSpace(draft.Language))
	draft.Title = strings.TrimSpace(draft.Title)
	draft.Body = strings.TrimSpace(draft.Body)
	draft.Visibility = strings.ToLower(strings.TrimSpace(draft.Visibility))
	draft.SourceKey = strings.ToLower(strings.TrimSpace(draft.SourceKey))
	draft.SourcePath = strings.TrimSpace(draft.SourcePath)
	draft.SourceRevision = strings.TrimSpace(draft.SourceRevision)
	draft.SourceURL = strings.TrimSpace(draft.SourceURL)
	draft.ImportedBy = strings.TrimSpace(draft.ImportedBy)
	if !validKnowledgeKey(draft.KnowledgeKey) || !validLanguage(draft.CanonicalLanguage) || !validLanguage(draft.Language) {
		return SourceResult{}, fmt.Errorf("%w: valid knowledge_key and languages are required", ErrInvalid)
	}
	if draft.Title == "" || draft.Body == "" || draft.SourceKey == "" || draft.SourcePath == "" || draft.SourceRevision == "" || draft.SourceURL == "" || draft.ImportedBy == "" {
		return SourceResult{}, fmt.Errorf("%w: source title, body, provenance, and importer are required", ErrInvalid)
	}
	if draft.Visibility != VisibilityShared && draft.Visibility != VisibilityRestricted {
		return SourceResult{}, fmt.Errorf("%w: visibility must be shared or restricted", ErrInvalid)
	}
	draft.ContentHash = fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(draft.Title+"\x00"+draft.Body)))
	return repository.UpsertSource(ctx, draft)
}

func Citation(knowledgeKey, language string, revisionNumber int64) string {
	return fmt.Sprintf("knowledge://%s?lang=%s&revision=%d", knowledgeKey, language, revisionNumber)
}

func normalizeDraft(draft ProposalDraft) ProposalDraft {
	draft.KnowledgeKey = strings.ToLower(strings.Trim(strings.TrimSpace(draft.KnowledgeKey), "/"))
	draft.CanonicalLanguage = strings.ToLower(strings.TrimSpace(draft.CanonicalLanguage))
	draft.Language = strings.ToLower(strings.TrimSpace(draft.Language))
	draft.ProposedBy = strings.TrimSpace(draft.ProposedBy)
	draft.Title = strings.TrimSpace(draft.Title)
	draft.Body = strings.TrimSpace(draft.Body)
	draft.BaseRevisionID = strings.TrimSpace(draft.BaseRevisionID)
	for index := range draft.Evidence {
		draft.Evidence[index].Source = strings.TrimSpace(draft.Evidence[index].Source)
		draft.Evidence[index].Reference = strings.TrimSpace(draft.Evidence[index].Reference)
		draft.Evidence[index].Summary = strings.TrimSpace(draft.Evidence[index].Summary)
	}
	return draft
}

func validateDraft(draft ProposalDraft) error {
	if !validKnowledgeKey(draft.KnowledgeKey) {
		return fmt.Errorf("%w: knowledge_key must be a lowercase path using letters, numbers, dots, dashes, or underscores", ErrInvalid)
	}
	if !validLanguage(draft.CanonicalLanguage) || !validLanguage(draft.Language) {
		return fmt.Errorf("%w: canonical_language and language are required", ErrInvalid)
	}
	if draft.ProposedBy == "" || draft.Title == "" || draft.Body == "" {
		return fmt.Errorf("%w: proposed_by, title, and body are required", ErrInvalid)
	}
	if len(draft.Evidence) == 0 {
		return fmt.Errorf("%w: at least one evidence reference is required", ErrInvalid)
	}
	if draft.BaseRevisionID != "" && !validUUID(draft.BaseRevisionID) {
		return fmt.Errorf("%w: base_revision_id must be a UUID", ErrInvalid)
	}
	for _, evidence := range draft.Evidence {
		if evidence.Source == "" || evidence.Reference == "" {
			return fmt.Errorf("%w: each evidence item requires source and reference", ErrInvalid)
		}
	}
	return nil
}

func validKnowledgeKey(value string) bool {
	if len(value) < 3 || len(value) > 200 || strings.Contains(value, "..") {
		return false
	}
	for _, character := range value {
		if unicode.IsLower(character) || unicode.IsDigit(character) || strings.ContainsRune("/-_.", character) {
			continue
		}
		return false
	}
	return true
}

func validLanguage(value string) bool {
	if len(value) < 2 || len(value) > 35 {
		return false
	}
	for _, character := range value {
		if unicode.IsLetter(character) || unicode.IsDigit(character) || character == '-' {
			continue
		}
		return false
	}
	return true
}

func validUUID(value string) bool {
	if len(value) != 36 {
		return false
	}
	for index, character := range value {
		if index == 8 || index == 13 || index == 18 || index == 23 {
			if character != '-' {
				return false
			}
			continue
		}
		if !strings.ContainsRune("0123456789abcdefABCDEF", character) {
			return false
		}
	}
	return true
}
