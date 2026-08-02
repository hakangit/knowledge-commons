CREATE TABLE knowledge_pages (
    id TEXT PRIMARY KEY,
    knowledge_key TEXT NOT NULL UNIQUE,
    canonical_language TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'published', 'deprecated')),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE knowledge_revisions (
    id TEXT PRIMARY KEY,
    page_id TEXT NOT NULL REFERENCES knowledge_pages(id) ON DELETE CASCADE,
    revision_number INTEGER NOT NULL,
    language TEXT NOT NULL,
    title TEXT NOT NULL,
    body TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('published', 'superseded')),
    authored_by TEXT NOT NULL,
    evidence TEXT NOT NULL,
    created_at TEXT NOT NULL,
    UNIQUE (page_id, language, revision_number)
);

CREATE INDEX knowledge_revisions_page_status_idx
    ON knowledge_revisions (page_id, language, status, revision_number DESC);

CREATE VIRTUAL TABLE knowledge_revisions_fts USING fts5(
    revision_id UNINDEXED,
    title,
    body,
    tokenize = 'unicode61'
);

CREATE TABLE knowledge_proposals (
    id TEXT PRIMARY KEY,
    page_id TEXT REFERENCES knowledge_pages(id),
    knowledge_key TEXT NOT NULL,
    canonical_language TEXT NOT NULL,
    base_revision_id TEXT REFERENCES knowledge_revisions(id),
    language TEXT NOT NULL,
    proposed_by TEXT NOT NULL,
    title TEXT NOT NULL,
    body TEXT NOT NULL,
    evidence TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'accepted', 'rejected')),
    reviewed_by TEXT,
    reviewed_at TEXT,
    decision_reason TEXT,
    created_at TEXT NOT NULL
);

CREATE INDEX knowledge_proposals_review_queue_idx
    ON knowledge_proposals (status, created_at);

CREATE TABLE knowledge_events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    event_type TEXT NOT NULL,
    actor_key TEXT NOT NULL,
    subject_type TEXT NOT NULL,
    subject_id TEXT,
    data TEXT NOT NULL,
    created_at TEXT NOT NULL
);

CREATE INDEX knowledge_events_subject_idx
    ON knowledge_events (subject_type, subject_id, id DESC);
