CREATE EXTENSION IF NOT EXISTS vector;

CREATE TABLE knowledge_pages (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    knowledge_key TEXT NOT NULL UNIQUE,
    owner_key TEXT,
    canonical_language TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'published', 'deprecated')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE knowledge_revisions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    page_id UUID NOT NULL REFERENCES knowledge_pages(id) ON DELETE CASCADE,
    revision_number BIGINT NOT NULL,
    language TEXT NOT NULL,
    title TEXT NOT NULL,
    body TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('draft', 'review', 'published', 'superseded', 'rejected')),
    authored_by TEXT NOT NULL,
    translated_from_revision_id UUID REFERENCES knowledge_revisions(id),
    provenance JSONB NOT NULL DEFAULT '{}'::jsonb,
    search_document TSVECTOR GENERATED ALWAYS AS (
        to_tsvector('simple', coalesce(title, '') || ' ' || coalesce(body, ''))
    ) STORED,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (page_id, language, revision_number)
);

CREATE INDEX knowledge_revisions_search_idx ON knowledge_revisions USING GIN (search_document);
CREATE INDEX knowledge_revisions_page_status_idx ON knowledge_revisions (page_id, status, revision_number DESC);

CREATE TABLE knowledge_chunks (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    revision_id UUID NOT NULL REFERENCES knowledge_revisions(id) ON DELETE CASCADE,
    ordinal INTEGER NOT NULL CHECK (ordinal >= 0),
    content TEXT NOT NULL,
    embedding_model TEXT,
    embedding_dimensions INTEGER,
    embedding VECTOR,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    search_document TSVECTOR GENERATED ALWAYS AS (to_tsvector('simple', coalesce(content, ''))) STORED,
    UNIQUE (revision_id, ordinal)
);

CREATE INDEX knowledge_chunks_search_idx ON knowledge_chunks USING GIN (search_document);

CREATE TABLE knowledge_nodes (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    external_key TEXT UNIQUE,
    kind TEXT NOT NULL,
    name TEXT NOT NULL,
    properties JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX knowledge_nodes_kind_idx ON knowledge_nodes (kind);

CREATE TABLE knowledge_edges (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    from_node_id UUID NOT NULL REFERENCES knowledge_nodes(id) ON DELETE CASCADE,
    relationship TEXT NOT NULL,
    to_node_id UUID NOT NULL REFERENCES knowledge_nodes(id) ON DELETE CASCADE,
    evidence_revision_id UUID REFERENCES knowledge_revisions(id),
    properties JSONB NOT NULL DEFAULT '{}'::jsonb,
    valid_from TIMESTAMPTZ NOT NULL DEFAULT now(),
    valid_to TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (from_node_id <> to_node_id),
    CHECK (valid_to IS NULL OR valid_to > valid_from)
);

CREATE INDEX knowledge_edges_from_idx ON knowledge_edges (from_node_id, relationship) WHERE valid_to IS NULL;
CREATE INDEX knowledge_edges_to_idx ON knowledge_edges (to_node_id, relationship) WHERE valid_to IS NULL;

CREATE TABLE knowledge_proposals (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    page_id UUID REFERENCES knowledge_pages(id),
    base_revision_id UUID REFERENCES knowledge_revisions(id),
    language TEXT NOT NULL,
    proposed_by TEXT NOT NULL,
    title TEXT,
    body TEXT NOT NULL,
    evidence JSONB NOT NULL DEFAULT '[]'::jsonb,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'accepted', 'rejected', 'withdrawn')),
    reviewed_by TEXT,
    reviewed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK ((status = 'pending' AND reviewed_at IS NULL) OR status <> 'pending')
);

CREATE INDEX knowledge_proposals_review_queue_idx ON knowledge_proposals (created_at) WHERE status = 'pending';

CREATE TABLE knowledge_events (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    event_type TEXT NOT NULL,
    actor_key TEXT NOT NULL,
    subject_type TEXT NOT NULL,
    subject_id UUID,
    data JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX knowledge_events_subject_idx ON knowledge_events (subject_type, subject_id, id DESC);
