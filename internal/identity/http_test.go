package identity

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestHeaderResolverRequiresAnExplicitActor(t *testing.T) {
	request := httptest.NewRequest("GET", "/", nil)
	_, err := (HeaderResolver{}).Resolve(request)
	if !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("expected unauthenticated error, got %v", err)
	}
}

func TestHeaderResolverDoesNotAcceptRolesFromTheRequest(t *testing.T) {
	request := httptest.NewRequest("GET", "/", nil)
	request.Header.Set("X-KC-Actor", "sales-user")
	request.Header.Set("X-KC-Roles", "director")
	principal, err := (HeaderResolver{}).Resolve(request)
	if err != nil {
		t.Fatalf("resolve principal: %v", err)
	}
	if principal.Actor != "sales-user" {
		t.Fatalf("actor = %q", principal.Actor)
	}
}

func TestRemoteResolverForwardsOnlyAgentCredentials(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Header.Get("Authorization") != "Bearer agent-key" {
			t.Fatal("authorization header was not forwarded")
		}
		if request.Header.Get("X-Acts-For") != "employee" {
			t.Fatal("acts-for header was not forwarded")
		}
		if request.Header.Get("Cookie") != "" || request.Header.Get("X-KC-Actor") != "" {
			t.Fatal("caller-controlled identity headers leaked upstream")
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"actor":"assistant","subject":"employee","is_agent":true}`)),
			Header:     make(http.Header),
		}, nil
	})}

	resolver, err := NewRemoteResolver("https://identity.example/auth/context", client)
	if err != nil {
		t.Fatalf("new remote resolver: %v", err)
	}
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("Authorization", "Bearer agent-key")
	request.Header.Set("X-Acts-For", "employee")
	request.Header.Set("Cookie", "session=secret")
	request.Header.Set("X-KC-Actor", "director")

	principal, err := resolver.Resolve(request)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if principal.Actor != "assistant" || principal.Subject != "employee" || !principal.IsAgent {
		t.Fatalf("principal = %#v", principal)
	}
}

func TestRemoteResolverPreservesIdentityRejection(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusForbidden,
			Body:       io.NopCloser(strings.NewReader("forbidden")),
			Header:     make(http.Header),
		}, nil
	})}
	resolver, err := NewRemoteResolver("https://identity.example/auth/context", client)
	if err != nil {
		t.Fatalf("new remote resolver: %v", err)
	}

	_, err = resolver.Resolve(httptest.NewRequest(http.MethodGet, "/", nil))
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected forbidden, got %v", err)
	}
}
