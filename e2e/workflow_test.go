//go:build e2e

package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"testing"

	"github.com/hakangit/knowledge-commons/internal/knowledge"
)

const testKnowledgeKey = "tests/proposal-publication"

func TestProposalPublicationWorkflow(t *testing.T) {
	baseURL := os.Getenv("KC_E2E_URL")
	if baseURL == "" {
		baseURL = "http://127.0.0.1:8080"
	}

	if os.Getenv("KC_E2E_VERIFY_ONLY") == "1" {
		verifyPublishedKnowledge(t, baseURL, 2, "spectral reference card")
		return
	}

	first := propose(t, baseURL, "agent-alex", knowledge.ProposalDraft{
		KnowledgeKey:      testKnowledgeKey,
		CanonicalLanguage: "en",
		Language:          "en",
		Title:             "Chromatic calibration procedure",
		Body:              "Chromatic calibration requires a clean reference surface. Quy trình cân chỉnh sắc độ phải dùng bề mặt chuẩn sạch.",
		Evidence: []knowledge.Evidence{{
			Source: "work-order", Reference: "WO-E2E-001", Summary: "Observed and verified during calibration",
		}},
	})

	review(t, baseURL, first.ID, "agent-alex", knowledge.Review{
		Decision: "accept", Reason: "self approval must fail",
	}, http.StatusConflict)

	firstPublished := review(t, baseURL, first.ID, "human-riley", knowledge.Review{
		Decision: "accept", Reason: "Procedure and evidence verified",
	}, http.StatusOK)
	if firstPublished.Revision == nil || firstPublished.Revision.RevisionNumber != 1 {
		t.Fatalf("first publication = %#v", firstPublished.Revision)
	}

	second := propose(t, baseURL, "agent-jordan", knowledge.ProposalDraft{
		KnowledgeKey:      testKnowledgeKey,
		CanonicalLanguage: "en",
		Language:          "en",
		Title:             "Chromatic calibration procedure",
		Body:              "Chromatic calibration requires a clean spectral reference card before measurement.",
		BaseRevisionID:    firstPublished.Revision.ID,
		Evidence: []knowledge.Evidence{{
			Source: "calibration-record", Reference: "CAL-E2E-002", Summary: "Reference card requirement confirmed",
		}},
	})

	secondPublished := review(t, baseURL, second.ID, "human-riley", knowledge.Review{
		Decision: "accept", Reason: "Correction verified against calibration record",
	}, http.StatusOK)
	if secondPublished.Revision == nil || secondPublished.Revision.RevisionNumber != 2 {
		t.Fatalf("second publication = %#v", secondPublished.Revision)
	}

	verifyPublishedKnowledge(t, baseURL, 2, "spectral reference card")
}

func propose(t *testing.T, baseURL, actor string, draft knowledge.ProposalDraft) knowledge.Proposal {
	t.Helper()
	var proposal knowledge.Proposal
	doJSONAs(t, http.MethodPost, baseURL+"/v1/proposals", draft, actor, http.StatusCreated, &proposal)
	if proposal.Status != "pending" || proposal.ID == "" {
		t.Fatalf("proposal = %#v", proposal)
	}
	return proposal
}

func review(t *testing.T, baseURL, proposalID, actor string, input knowledge.Review, expectedStatus int) knowledge.ReviewResult {
	t.Helper()
	var result knowledge.ReviewResult
	doJSONAs(t, http.MethodPost, baseURL+"/v1/proposals/"+proposalID+"/review", input, actor, expectedStatus, &result)
	return result
}

func verifyPublishedKnowledge(t *testing.T, baseURL string, revisionNumber int64, query string) {
	t.Helper()
	pageURL := baseURL + "/v1/pages?key=" + url.QueryEscape(testKnowledgeKey) + "&language=en"
	var revision knowledge.Revision
	doJSONAs(t, http.MethodGet, pageURL, nil, "human-riley", http.StatusOK, &revision)
	if revision.RevisionNumber != revisionNumber {
		t.Fatalf("revision number = %d", revision.RevisionNumber)
	}
	if len(revision.Evidence) != 1 || revision.Citation == "" {
		t.Fatalf("published provenance = %#v, citation = %q", revision.Evidence, revision.Citation)
	}

	searchURL := baseURL + "/v1/search?q=" + url.QueryEscape(query) + "&language=en"
	var response struct {
		Results []knowledge.SearchResult `json:"results"`
	}
	doJSONAs(t, http.MethodGet, searchURL, nil, "human-riley", http.StatusOK, &response)
	if len(response.Results) == 0 || response.Results[0].KnowledgeKey != testKnowledgeKey {
		t.Fatalf("search results = %#v", response.Results)
	}
	if response.Results[0].Citation != revision.Citation {
		t.Fatalf("search citation = %q, page citation = %q", response.Results[0].Citation, revision.Citation)
	}
}

func doJSON(t *testing.T, method, endpoint string, requestBody any, expectedStatus int, responseBody any) {
	t.Helper()
	doJSONAs(t, method, endpoint, requestBody, "", expectedStatus, responseBody)
}

func doJSONAs(
	t *testing.T,
	method, endpoint string,
	requestBody any,
	actor string,
	expectedStatus int,
	responseBody any,
) {
	t.Helper()
	var body io.Reader
	if requestBody != nil {
		encoded, err := json.Marshal(requestBody)
		if err != nil {
			t.Fatalf("encode request: %v", err)
		}
		body = bytes.NewReader(encoded)
	}

	request, err := http.NewRequest(method, endpoint, body)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	if actor != "" {
		request.Header.Set("X-KC-Actor", actor)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("request %s: %v", endpoint, err)
	}
	defer response.Body.Close()

	contents, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	if response.StatusCode != expectedStatus {
		t.Fatalf("%s: status %d, expected %d: %s", endpoint, response.StatusCode, expectedStatus, contents)
	}
	if responseBody != nil && expectedStatus < 400 {
		if err := json.Unmarshal(contents, responseBody); err != nil {
			t.Fatalf("decode response %q: %v", contents, err)
		}
	}
	if expectedStatus >= 400 && len(contents) == 0 {
		t.Fatal(fmt.Sprintf("%s returned an empty error response", endpoint))
	}
}
