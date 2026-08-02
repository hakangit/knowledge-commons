package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/hakangit/knowledge-commons/internal/identity"
	"github.com/hakangit/knowledge-commons/internal/knowledge"
)

type ReadinessChecker interface {
	Ping(context.Context) error
}

type KnowledgeOperations interface {
	Propose(context.Context, knowledge.ProposalDraft) (knowledge.Proposal, error)
	Review(context.Context, string, knowledge.Review) (knowledge.ReviewResult, error)
	GetPublished(context.Context, string, string) (knowledge.Revision, error)
	Search(context.Context, string, string, int) ([]knowledge.SearchResult, error)
}

type VisibleKnowledgeOperations interface {
	GetPublishedVisible(context.Context, string, string, bool) (knowledge.Revision, error)
	SearchVisible(context.Context, string, string, int, bool) ([]knowledge.SearchResult, error)
}

type SourceOperations interface {
	ImportSource(context.Context, knowledge.SourceDraft) (knowledge.SourceResult, error)
}

type AccessPolicy struct {
	restrictedSubjects map[string]struct{}
	ingestSubjects     map[string]struct{}
}

func NewAccessPolicy(restrictedSubjects, ingestSubjects []string) AccessPolicy {
	policy := AccessPolicy{
		restrictedSubjects: make(map[string]struct{}, len(restrictedSubjects)),
		ingestSubjects:     make(map[string]struct{}, len(ingestSubjects)),
	}
	for _, subject := range restrictedSubjects {
		policy.restrictedSubjects[subject] = struct{}{}
	}
	for _, subject := range ingestSubjects {
		policy.ingestSubjects[subject] = struct{}{}
	}
	return policy
}

func (policy AccessPolicy) canReadRestricted(principal identity.Principal) bool {
	_, allowed := policy.restrictedSubjects[principal.Subject]
	return allowed
}

func (policy AccessPolicy) canIngest(principal identity.Principal) bool {
	_, allowed := policy.ingestSubjects[principal.Subject]
	return allowed
}

type Server struct {
	httpServer *http.Server
}

func New(
	address, version string,
	readiness ReadinessChecker,
	operations KnowledgeOperations,
	principals identity.Resolver,
) *Server {
	return NewWithSources(address, version, readiness, operations, nil, principals, AccessPolicy{})
}

func NewWithSources(
	address, version string,
	readiness ReadinessChecker,
	operations KnowledgeOperations,
	sources SourceOperations,
	principals identity.Resolver,
	policy AccessPolicy,
) *Server {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /livez", writeJSON(http.StatusOK, map[string]string{"status": "ok"}))
	mux.HandleFunc("GET /readyz", func(response http.ResponseWriter, request *http.Request) {
		ctx, cancel := context.WithTimeout(request.Context(), time.Second)
		defer cancel()

		if err := readiness.Ping(ctx); err != nil {
			writeJSON(http.StatusServiceUnavailable, map[string]string{"status": "unavailable"})(response, request)
			return
		}
		writeJSON(http.StatusOK, map[string]string{"status": "ready"})(response, request)
	})
	mux.HandleFunc("GET /v1/info", writeJSON(http.StatusOK, map[string]string{
		"name":    "knowledge-commons",
		"version": version,
	}))
	mux.HandleFunc("GET /v1/principal", func(response http.ResponseWriter, request *http.Request) {
		principal, err := principals.Resolve(request)
		if err != nil {
			writeError(response, request, err)
			return
		}
		writeJSON(http.StatusOK, principal)(response, request)
	})
	mux.HandleFunc("POST /v1/proposals", func(response http.ResponseWriter, request *http.Request) {
		principal, err := principals.Resolve(request)
		if err != nil {
			writeError(response, request, err)
			return
		}
		var draft knowledge.ProposalDraft
		if err := decodeJSON(response, request, &draft); err != nil {
			writeError(response, request, fmt.Errorf("%w: %v", knowledge.ErrInvalid, err))
			return
		}
		draft.ProposedBy = principal.Actor
		proposal, err := operations.Propose(request.Context(), draft)
		if err != nil {
			writeError(response, request, err)
			return
		}
		writeJSON(http.StatusCreated, proposal)(response, request)
	})
	mux.HandleFunc("POST /v1/proposals/{proposalID}/review", func(response http.ResponseWriter, request *http.Request) {
		principal, err := principals.Resolve(request)
		if err != nil {
			writeError(response, request, err)
			return
		}
		var review knowledge.Review
		if err := decodeJSON(response, request, &review); err != nil {
			writeError(response, request, fmt.Errorf("%w: %v", knowledge.ErrInvalid, err))
			return
		}
		review.ReviewedBy = principal.Actor
		result, err := operations.Review(request.Context(), request.PathValue("proposalID"), review)
		if err != nil {
			writeError(response, request, err)
			return
		}
		writeJSON(http.StatusOK, result)(response, request)
	})
	mux.HandleFunc("GET /v1/pages", func(response http.ResponseWriter, request *http.Request) {
		principal, err := principals.Resolve(request)
		if err != nil {
			writeError(response, request, err)
			return
		}
		var revision knowledge.Revision
		if visible, ok := operations.(VisibleKnowledgeOperations); ok {
			revision, err = visible.GetPublishedVisible(
				request.Context(), request.URL.Query().Get("key"), request.URL.Query().Get("language"),
				policy.canReadRestricted(principal),
			)
		} else {
			revision, err = operations.GetPublished(
				request.Context(), request.URL.Query().Get("key"), request.URL.Query().Get("language"),
			)
		}
		if err != nil {
			writeError(response, request, err)
			return
		}
		writeJSON(http.StatusOK, revision)(response, request)
	})
	mux.HandleFunc("GET /v1/search", func(response http.ResponseWriter, request *http.Request) {
		principal, err := principals.Resolve(request)
		if err != nil {
			writeError(response, request, err)
			return
		}
		limit := 0
		if raw := request.URL.Query().Get("limit"); raw != "" {
			parsed, parseErr := strconv.Atoi(raw)
			if parseErr != nil {
				writeError(response, request, fmt.Errorf("%w: limit must be an integer", knowledge.ErrInvalid))
				return
			}
			limit = parsed
		}
		var results []knowledge.SearchResult
		if visible, ok := operations.(VisibleKnowledgeOperations); ok {
			results, err = visible.SearchVisible(
				request.Context(), request.URL.Query().Get("q"), request.URL.Query().Get("language"), limit,
				policy.canReadRestricted(principal),
			)
		} else {
			results, err = operations.Search(
				request.Context(), request.URL.Query().Get("q"), request.URL.Query().Get("language"), limit,
			)
		}
		if err != nil {
			writeError(response, request, err)
			return
		}
		writeJSON(http.StatusOK, map[string]any{"results": results})(response, request)
	})
	if sources != nil {
		mux.HandleFunc("PUT /v1/source-documents", func(response http.ResponseWriter, request *http.Request) {
			principal, err := principals.Resolve(request)
			if err != nil {
				writeError(response, request, err)
				return
			}
			if !policy.canIngest(principal) {
				writeError(response, request, fmt.Errorf("%w: source ingestion is not allowed", identity.ErrForbidden))
				return
			}
			var draft knowledge.SourceDraft
			if err := decodeJSON(response, request, &draft); err != nil {
				writeError(response, request, fmt.Errorf("%w: %v", knowledge.ErrInvalid, err))
				return
			}
			draft.ImportedBy = principal.Actor
			result, err := sources.ImportSource(request.Context(), draft)
			if err != nil {
				writeError(response, request, err)
				return
			}
			writeJSON(http.StatusOK, result)(response, request)
		})
	}
	return &Server{httpServer: &http.Server{
		Addr:              address,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}}
}

func (server *Server) Handler() http.Handler {
	return server.httpServer.Handler
}

func (server *Server) ListenAndServe() error {
	err := server.httpServer.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (server *Server) Shutdown(ctx context.Context) error {
	return server.httpServer.Shutdown(ctx)
}

func writeJSON(status int, value any) http.HandlerFunc {
	return func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		response.WriteHeader(status)
		_ = json.NewEncoder(response).Encode(value)
	}
}

func decodeJSON(response http.ResponseWriter, request *http.Request, destination any) error {
	request.Body = http.MaxBytesReader(response, request.Body, 1<<20)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain one JSON object")
	}
	return nil
}

func writeError(response http.ResponseWriter, request *http.Request, err error) {
	status := http.StatusInternalServerError
	message := "internal server error"
	switch {
	case errors.Is(err, identity.ErrUnauthenticated):
		status = http.StatusUnauthorized
		message = err.Error()
	case errors.Is(err, identity.ErrForbidden):
		status = http.StatusForbidden
		message = err.Error()
	case errors.Is(err, identity.ErrUnavailable):
		status = http.StatusServiceUnavailable
		message = err.Error()
	case errors.Is(err, knowledge.ErrInvalid):
		status = http.StatusBadRequest
		message = err.Error()
	case errors.Is(err, knowledge.ErrNotFound):
		status = http.StatusNotFound
		message = err.Error()
	case errors.Is(err, knowledge.ErrConflict):
		status = http.StatusConflict
		message = err.Error()
	default:
		slog.Error("request failed", "method", request.Method, "path", request.URL.Path, "error", err)
	}
	writeJSON(status, map[string]string{"error": message})(response, request)
}
