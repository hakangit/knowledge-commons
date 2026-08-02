package identity

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var (
	ErrUnauthenticated = errors.New("authentication required")
	ErrForbidden       = errors.New("identity rejected the request")
	ErrUnavailable     = errors.New("identity provider unavailable")
)

type Principal struct {
	Actor   string `json:"actor"`
	Subject string `json:"subject"`
	IsAgent bool   `json:"is_agent"`
}

type Resolver interface {
	Resolve(*http.Request) (Principal, error)
}

type DisabledResolver struct{}

func (DisabledResolver) Resolve(*http.Request) (Principal, error) {
	return Principal{}, ErrUnauthenticated
}

type HeaderResolver struct{}

func (HeaderResolver) Resolve(request *http.Request) (Principal, error) {
	actor := strings.TrimSpace(request.Header.Get("X-KC-Actor"))
	if actor == "" {
		return Principal{}, ErrUnauthenticated
	}
	return Principal{Actor: actor, Subject: actor}, nil
}

type RemoteResolver struct {
	endpoint string
	client   *http.Client
}

func NewRemoteResolver(endpoint string, client *http.Client) (*RemoteResolver, error) {
	parsed, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("identity endpoint must be an absolute URL")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("identity endpoint must use http or https")
	}
	if client == nil {
		client = &http.Client{
			Timeout: 5 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
	}
	return &RemoteResolver{endpoint: parsed.String(), client: client}, nil
}

func (resolver *RemoteResolver) Resolve(request *http.Request) (Principal, error) {
	upstream, err := http.NewRequestWithContext(request.Context(), http.MethodGet, resolver.endpoint, nil)
	if err != nil {
		return Principal{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	for _, header := range []string{"Authorization", "X-Acts-For", "Accept-Language"} {
		if value := request.Header.Get(header); value != "" {
			upstream.Header.Set(header, value)
		}
	}

	response, err := resolver.client.Do(upstream)
	if err != nil {
		return Principal{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer response.Body.Close()
	switch response.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized:
		return Principal{}, ErrUnauthenticated
	case http.StatusForbidden, http.StatusBadRequest:
		return Principal{}, ErrForbidden
	default:
		return Principal{}, fmt.Errorf("%w: status %d", ErrUnavailable, response.StatusCode)
	}

	var principal Principal
	decoder := json.NewDecoder(io.LimitReader(response.Body, 64<<10))
	if err := decoder.Decode(&principal); err != nil {
		return Principal{}, fmt.Errorf("%w: invalid response: %v", ErrUnavailable, err)
	}
	principal.Actor = strings.TrimSpace(principal.Actor)
	principal.Subject = strings.TrimSpace(principal.Subject)
	if principal.Actor == "" || principal.Subject == "" {
		return Principal{}, fmt.Errorf("%w: identity response omitted actor or subject", ErrUnavailable)
	}
	return principal, nil
}
