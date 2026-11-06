// Copyright IBM Corp. 2021, 2026
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestGetCollective(t *testing.T) {
	tests := map[string]struct {
		lookup   CollectiveLookup
		wantVars map[string]any
	}{
		"id":            {CollectiveLookup{ID: "abc"}, map[string]any{"id": "abc"}},
		"slug":          {CollectiveLookup{Slug: "babel"}, map[string]any{"slug": "babel"}},
		"github handle": {CollectiveLookup{GithubHandle: "babel"}, map[string]any{"githubHandle": "babel"}},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req Request
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Fatalf("decoding request: %v", err)
				}
				if req.OperationName != "GetCollective" {
					t.Errorf("operationName = %q", req.OperationName)
				}
				if !reflect.DeepEqual(req.Variables, tc.wantVars) {
					t.Errorf("variables = %v, want %v", req.Variables, tc.wantVars)
				}

				_, _ = w.Write([]byte(`{"data":{"collective":{
					"id":"abc","legacyId":580,"slug":"babel","type":"COLLECTIVE","name":"Babel",
					"currency":"USD","tags":["javascript"],"isActive":true,"isApproved":true,"isArchived":false,
					"socialLinks":[{"type":"GITHUB","url":"https://github.com/babel"}],
					"host":{"id":"h1","slug":"opensource"}
				}}}`))
			}))
			defer srv.Close()

			got, err := New(srv.URL).GetCollective(context.Background(), tc.lookup)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Slug != "babel" || got.LegacyID != 580 || *got.Name != "Babel" || got.Host.Slug != "opensource" {
				t.Errorf("unexpected collective: %+v", got)
			}
			if got.Description != nil {
				t.Errorf("description = %v, want nil", *got.Description)
			}
		})
	}
}

func TestGetCollective_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"collective":null}}`))
	}))
	defer srv.Close()

	_, err := New(srv.URL).GetCollective(context.Background(), CollectiveLookup{Slug: "nope"})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestGetCollective_NoLookup(t *testing.T) {
	if _, err := New("http://unused.invalid").GetCollective(context.Background(), CollectiveLookup{}); err == nil {
		t.Fatal("expected error for empty lookup")
	}
}
