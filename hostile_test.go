package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// hostile replies use the wrong type for every field, which is what a
// changed or broken api returns
func hostileHandler(w http.ResponseWriter, r *http.Request) {
	_ = json.NewEncoder(w).Encode(map[string]any{
		"data": map[string]any{
			"viewer": map[string]any{
				"login": 42,
				"name":  []any{"nope"},
				"repositories": map[string]any{
					"pageInfo": map[string]any{"hasNextPage": "maybe", "endCursor": 7},
					"nodes": []any{
						"not a map",
						nil,
						map[string]any{
							"nameWithOwner": 9,
							"forkCount":     "many",
							"stargazers":    []any{1, 2},
							"languages":     "kotlin",
						},
						map[string]any{
							"nameWithOwner": "me/repo",
							"forkCount":     nil,
							"languages": map[string]any{"edges": []any{
								nil,
								"flat",
								map[string]any{"size": "big", "node": "no"},
								map[string]any{"size": 10, "node": map[string]any{"name": 5}},
							}},
						},
					},
				},
				"repositoriesContributedTo": map[string]any{
					"pageInfo": map[string]any{"hasNextPage": true, "endCursor": nil},
					"nodes":    []any{map[string]any{"nameWithOwner": "me/other"}},
				},
			},
		},
	})
}

func fastRetries(t *testing.T) {
	t.Helper()
	before := retryBaseDelay
	retryBaseDelay = 0
	t.Cleanup(func() { retryBaseDelay = before })
}

func TestHostileJSONDoesNotPanic(t *testing.T) {
	fastRetries(t)
	useTempDirs(t)
	server := httptest.NewServer(http.HandlerFunc(hostileHandler))
	t.Cleanup(server.Close)
	GraphQLURL = server.URL
	RESTURL = server.URL

	stats := NewStats("me", NewClient("me", "x"))
	stats.ExcludeLangs = map[string]bool{"kotlin": true}

	if err := safeErr(func() error { return stats.Load(context.Background()) }); err != nil {
		t.Fatalf("load failed on hostile input: %v", err)
	}
	if err := safeErr(func() error { return stats.LoadLinesChanged(context.Background()) }); err != nil {
		t.Fatalf("lines failed: %v", err)
	}
	if err := safeErr(func() error { return stats.LoadViews(context.Background()) }); err != nil {
		t.Fatalf("views failed: %v", err)
	}
	if err := safeErr(func() error { return stats.LoadContributions(context.Background()) }); err != nil {
		t.Fatalf("contributions failed: %v", err)
	}
	if err := safeErr(func() error { return GenerateLanguages(stats) }); err != nil {
		t.Fatalf("languages card failed: %v", err)
	}
	if err := safeErr(func() error { return GenerateOverview(stats) }); err != nil {
		t.Fatalf("overview card failed: %v", err)
	}
}

func TestEmptyJSONResponses(t *testing.T) {
	fastRetries(t)
	for _, body := range []string{"null", "{}", "[]", `{"data":null}`, `{"data":{"viewer":null}}`, ""} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(body))
		}))
		GraphQLURL = server.URL
		RESTURL = server.URL

		stats := NewStats("me", NewClient("me", "x"))
		err := safeErr(func() error { return stats.Load(context.Background()) })
		if err == nil {
			t.Errorf("body %q: expected an error, got a silent pass", body)
		}
		server.Close()
	}
}
