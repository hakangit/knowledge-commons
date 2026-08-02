package main

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/hakangit/knowledge-commons/internal/knowledge"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestMarkdownImportKeepsCanonicalTitleAndBody(t *testing.T) {
	title, body := parseMarkdown("---\ntitle: 'Jet dyeing'\nweight: 2\n---\n# Lesson\n\nReach 130 C.", "jet.md")
	if title != "Jet dyeing" || body != "# Lesson\n\nReach 130 C." {
		t.Fatalf("title=%q body=%q", title, body)
	}
}

func TestKnowledgeKeyNormalizesLegacyWikiPaths(t *testing.T) {
	key := normalizeKnowledgeKey("tech-wiki/IoT/Relay Boards")
	if key != "tech-wiki/iot/relay-boards" {
		t.Fatalf("key = %q", key)
	}
}

func TestSourceSyncRetriesTransientIdentityFailuresBecausePUTIsIdempotent(t *testing.T) {
	attempts := 0
	client := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		attempts++
		if attempts < 3 {
			return &http.Response{
				StatusCode: http.StatusServiceUnavailable,
				Body:       io.NopCloser(strings.NewReader("identity unavailable")),
			}, nil
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"changed":true,"revision":{}}`)),
		}, nil
	})}

	result, err := putSource(client, "https://knowledge.example", "token", "admin", "", knowledge.SourceDraft{})
	if err != nil || !result.Changed || attempts != 3 {
		t.Fatalf("result=%#v attempts=%d error=%v", result, attempts, err)
	}
}
