// Copyright IBM Corp. 2021, 2026
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestDo_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if got := r.Header.Get("Personal-Token"); got != "secret" {
			t.Errorf("Personal-Token = %q, want %q", got, "secret")
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q", got)
		}

		var req Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decoding request: %v", err)
		}
		if req.Variables["slug"] != "babel" {
			t.Errorf("variables = %v", req.Variables)
		}

		_, _ = w.Write([]byte(`{"data":{"account":{"name":"Babel"}}}`))
	}))
	defer srv.Close()

	c := New(srv.URL, WithToken("secret"))

	var out struct {
		Account struct {
			Name string `json:"name"`
		} `json:"account"`
	}

	err := c.Do(context.Background(), `query($slug: String) { account(slug: $slug) { name } }`, map[string]any{"slug": "babel"}, &out)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Account.Name != "Babel" {
		t.Errorf("name = %q, want Babel", out.Account.Name)
	}
}

func TestDo_NoTokenHeaderWhenUnset(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := r.Header["Personal-Token"]; ok {
			t.Error("Personal-Token header should not be set")
		}
		_, _ = w.Write([]byte(`{"data":{}}`))
	}))
	defer srv.Close()

	if err := New(srv.URL).Do(context.Background(), `{ __typename }`, nil, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDo_GraphQLErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"account":null},"errors":[{"message":"Account Not Found","path":["account"]}]}`))
	}))
	defer srv.Close()

	err := New(srv.URL).Do(context.Background(), `{ account { name } }`, nil, &struct{}{})

	var gqlErrs Errors
	if !errors.As(err, &gqlErrs) {
		t.Fatalf("expected Errors, got %T: %v", err, err)
	}
	if len(gqlErrs) != 1 || gqlErrs[0].Message != "Account Not Found" {
		t.Errorf("errors = %+v", gqlErrs)
	}
	if want := "graphql: Account Not Found (path: account)"; err.Error() != want {
		t.Errorf("Error() = %q, want %q", err.Error(), want)
	}
}

func TestDo_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad gateway", http.StatusBadGateway)
	}))
	defer srv.Close()

	err := New(srv.URL, WithRetry(0, 0)).Do(context.Background(), `{ __typename }`, nil, nil)

	var httpErr *HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("expected *HTTPError, got %T: %v", err, err)
	}
	if httpErr.StatusCode != http.StatusBadGateway {
		t.Errorf("status = %d", httpErr.StatusCode)
	}
}

func TestNew_DefaultEndpoint(t *testing.T) {
	if got := New("").Endpoint(); got != DefaultEndpoint {
		t.Errorf("endpoint = %q, want %q", got, DefaultEndpoint)
	}
}

func TestDo_RetriesRateLimit(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) < 3 {
			w.Header().Set("Retry-After", "0.01")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"message":"Rate limit exceeded"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":{"ok":true}}`))
	}))
	defer srv.Close()

	var out struct{ OK bool }
	if err := New(srv.URL).Do(context.Background(), `{ ok }`, nil, &out); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !out.OK || calls.Load() != 3 {
		t.Errorf("ok = %v, calls = %d, want true, 3", out.OK, calls.Load())
	}
}

func TestDo_RetriesExhausted(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	err := New(srv.URL, WithRetry(2, time.Millisecond)).Do(context.Background(), `{ ok }`, nil, nil)

	var httpErr *HTTPError
	if !errors.As(err, &httpErr) || httpErr.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("err = %v, want 429 HTTPError", err)
	}
	if calls.Load() != 3 {
		t.Errorf("calls = %d, want 3", calls.Load())
	}
}

func TestDo_RetryRespectsContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := New(srv.URL).Do(ctx, `{ ok }`, nil, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want deadline exceeded", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Error("retry wait ignored context cancellation")
	}
}

func TestRetryWait(t *testing.T) {
	tests := map[string]struct {
		header  string
		attempt int
		want    time.Duration
	}{
		"fractional seconds": {"16.5", 0, 16500 * time.Millisecond},
		"integer seconds":    {"3", 0, 3 * time.Second},
		"capped":             {"120", 0, time.Minute},
		"backoff no header":  {"", 2, 4 * time.Second},
		"backoff capped":     {"", 10, time.Minute},
		"garbage header":     {"soon", 1, 2 * time.Second},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			if got := retryWait(tc.header, tc.attempt, time.Minute); got != tc.want {
				t.Errorf("retryWait(%q, %d) = %v, want %v", tc.header, tc.attempt, got, tc.want)
			}
		})
	}
}
