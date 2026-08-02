ALTER TABLE knowledge_pages ADD COLUMN kind TEXT NOT NULL DEFAULT 'reviewed';
ALTER TABLE knowledge_pages ADD COLUMN visibility TEXT NOT NULL DEFAULT 'shared';
ALTER TABLE knowledge_pages ADD COLUMN source_key TEXT;
ALTER TABLE knowledge_pages ADD COLUMN source_path TEXT;

ALTER TABLE knowledge_revisions ADD COLUMN source_revision TEXT;
ALTER TABLE knowledge_revisions ADD COLUMN source_url TEXT;
ALTER TABLE knowledge_revisions ADD COLUMN content_hash TEXT;

CREATE TABLE knowledge_chunks (
    id TEXT PRIMARY KEY,
    revision_id TEXT NOT NULL REFERENCES knowledge_revisions(id) ON DELETE CASCADE,
    ordinal INTEGER NOT NULL,
    heading TEXT NOT NULL,
    body TEXT NOT NULL,
    UNIQUE (revision_id, ordinal)
);

CREATE VIRTUAL TABLE knowledge_chunks_fts USING fts5(
    chunk_id UNINDEXED,
    title,
    heading,
    body,
    tokenize = 'unicode61'
);

INSERT INTO knowledge_chunks (id, revision_id, ordinal, heading, body)
SELECT lower(hex(randomblob(4))) || '-' || lower(hex(randomblob(2))) || '-4' ||
       substr(lower(hex(randomblob(2))), 2) || '-a' || substr(lower(hex(randomblob(2))), 2) || '-' ||
       lower(hex(randomblob(6))), id, 0, '', body
FROM knowledge_revisions;

INSERT INTO knowledge_chunks_fts (chunk_id, title, heading, body)
SELECT chunk.id, revision.title, chunk.heading, chunk.body
FROM knowledge_chunks AS chunk
JOIN knowledge_revisions AS revision ON revision.id = chunk.revision_id;

CREATE INDEX knowledge_pages_source_idx ON knowledge_pages (source_key, source_path, visibility);
CREATE INDEX knowledge_revisions_content_hash_idx ON knowledge_revisions (content_hash);
