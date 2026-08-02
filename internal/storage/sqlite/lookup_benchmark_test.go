package sqlite

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hakangit/knowledge-commons/internal/knowledge"
)

type benchmarkChunk struct {
	language string
	text     string
}

func BenchmarkLookupStrategies(b *testing.B) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(b.TempDir(), "knowledge.db"))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(store.Close)
	if err := store.Migrate(ctx); err != nil {
		b.Fatal(err)
	}

	documentCount := seedSyntheticBenchmarkCorpus(b, ctx, store)
	chunks := loadBenchmarkChunks(b, ctx, store)
	terms := []string{"polyester", "pressure", "vessel"}

	ftsResults, err := store.SearchVisible(ctx, strings.Join(terms, " "), "en", 3, true)
	if err != nil || len(ftsResults) != 3 {
		b.Fatalf("FTS validation returned %d results: %v", len(ftsResults), err)
	}
	if got := likeLookup(ctx, b, store, terms); got != 3 {
		b.Fatalf("LIKE validation returned %d results", got)
	}
	if got := linearLookup(chunks, terms); got != 3 {
		b.Fatalf("linear validation returned %d results", got)
	}

	b.Logf("corpus: %d documents, %d heading chunks", documentCount, len(chunks))
	b.Run("SQLite_FTS5_BM25", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			results, err := store.SearchVisible(ctx, strings.Join(terms, " "), "en", 3, true)
			if err != nil || len(results) != 3 {
				b.Fatalf("search returned %d results: %v", len(results), err)
			}
		}
	})
	b.Run("SQLite_LIKE_no_ranking", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if got := likeLookup(ctx, b, store, terms); got != 3 {
				b.Fatalf("LIKE returned %d results", got)
			}
		}
	})
	b.Run("Go_linear_scan_no_ranking", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if got := linearLookup(chunks, terms); got != 3 {
				b.Fatalf("linear scan returned %d results", got)
			}
		}
	})
}

func seedSyntheticBenchmarkCorpus(b *testing.B, ctx context.Context, store *Store) int {
	b.Helper()
	service := knowledge.NewService(store)
	languages := []string{"en", "vi", "zh-tw"}
	hits := map[int]bool{9: true, 198: true, 399: true}
	for document := 0; document < 579; document++ {
		sections := 13
		if document < 324 {
			sections = 14
		}
		var body strings.Builder
		fmt.Fprintf(&body, "# Reference page %03d\n\nSynthetic public benchmark material.\n", document)
		for section := 0; section < sections; section++ {
			fmt.Fprintf(&body, "\n## Procedure %02d\n\n", section)
			body.WriteString("Documented operating procedure with safety checks, ownership, evidence, and revision history. ")
			body.WriteString("The responsible team validates inputs before publishing the approved result. ")
			if hits[document] && section == 7 {
				body.WriteString("Polyester processing requires a pressure vessel for the validated high-temperature cycle. ")
			}
		}
		language := languages[document%len(languages)]
		_, err := service.ImportSource(ctx, knowledge.SourceDraft{
			KnowledgeKey: fmt.Sprintf("benchmark/page-%03d", document), CanonicalLanguage: "en",
			Language: language, Title: fmt.Sprintf("Reference page %03d", document), Body: body.String(),
			Visibility: knowledge.VisibilityShared, SourceKey: "synthetic",
			SourcePath: fmt.Sprintf("%s/page-%03d.md", language, document), SourceRevision: "benchmark",
			SourceURL:  fmt.Sprintf("https://benchmark.invalid/%s/page-%03d.md", language, document),
			ImportedBy: "benchmark",
		})
		if err != nil {
			b.Fatalf("import synthetic document %d: %v", document, err)
		}
	}
	return 579
}

func loadBenchmarkChunks(b *testing.B, ctx context.Context, store *Store) []benchmarkChunk {
	b.Helper()
	rows, err := store.db.QueryContext(ctx, `
		SELECT revision.language, lower(revision.title || ' ' || chunk.heading || ' ' || chunk.body)
		FROM knowledge_chunks AS chunk
		JOIN knowledge_revisions AS revision ON revision.id = chunk.revision_id
		WHERE revision.status = 'published'`)
	if err != nil {
		b.Fatal(err)
	}
	defer rows.Close()
	var chunks []benchmarkChunk
	for rows.Next() {
		var chunk benchmarkChunk
		if err := rows.Scan(&chunk.language, &chunk.text); err != nil {
			b.Fatal(err)
		}
		chunks = append(chunks, chunk)
	}
	if err := rows.Err(); err != nil {
		b.Fatal(err)
	}
	return chunks
}

func likeLookup(ctx context.Context, b *testing.B, store *Store, terms []string) int {
	b.Helper()
	rows, err := store.db.QueryContext(ctx, `
		SELECT chunk.id
		FROM knowledge_chunks AS chunk
		JOIN knowledge_revisions AS revision ON revision.id = chunk.revision_id
		WHERE revision.status = 'published' AND revision.language = 'en'
		  AND lower(revision.title || ' ' || chunk.heading || ' ' || chunk.body) LIKE ?
		  AND lower(revision.title || ' ' || chunk.heading || ' ' || chunk.body) LIKE ?
		  AND lower(revision.title || ' ' || chunk.heading || ' ' || chunk.body) LIKE ?
		LIMIT 3`, "%"+terms[0]+"%", "%"+terms[1]+"%", "%"+terms[2]+"%")
	if err != nil {
		b.Fatal(err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			b.Fatal(err)
		}
		count++
	}
	if err := rows.Err(); err != nil {
		b.Fatal(err)
	}
	return count
}

func linearLookup(chunks []benchmarkChunk, terms []string) int {
	count := 0
	for _, chunk := range chunks {
		if chunk.language != "en" {
			continue
		}
		matched := true
		for _, term := range terms {
			if !strings.Contains(chunk.text, term) {
				matched = false
				break
			}
		}
		if matched {
			count++
			if count == 3 {
				return count
			}
		}
	}
	return count
}
