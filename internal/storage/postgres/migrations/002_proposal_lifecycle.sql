CREATE EXTENSION IF NOT EXISTS pg_trgm;

ALTER TABLE knowledge_proposals ADD COLUMN knowledge_key TEXT;
ALTER TABLE knowledge_proposals ADD COLUMN canonical_language TEXT;
ALTER TABLE knowledge_proposals ADD COLUMN decision_reason TEXT;

UPDATE knowledge_proposals AS proposal
SET knowledge_key = page.knowledge_key,
    canonical_language = page.canonical_language
FROM knowledge_pages AS page
WHERE proposal.page_id = page.id;

CREATE INDEX knowledge_revisions_title_trgm_idx ON knowledge_revisions USING GIN (lower(title) gin_trgm_ops);
CREATE INDEX knowledge_revisions_body_trgm_idx ON knowledge_revisions USING GIN (lower(body) gin_trgm_ops);
CREATE INDEX knowledge_proposals_key_idx ON knowledge_proposals (knowledge_key, created_at DESC);
