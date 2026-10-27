package main

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestErrorsDoNotNameRepos(t *testing.T) {
	GraphQLURL = "http://127.0.0.1:1/graphql"
	RESTURL = "http://127.0.0.1:1"
	client := NewClient("me", "x")

	_, err := client.GraphQL(context.Background(), "{viewer{login}}")
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), "127.0.0.1") {
		t.Fatalf("error names the host: %v", err)
	}

	_, err = client.REST(context.Background(), "/repos/me/secret-repo/traffic/views", nil, 1)
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), "secret-repo") {
		t.Fatalf("error names the repo: %v", err)
	}
}

func TestGraphQLErrorDropsRepoName(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"errors": []any{
				map[string]any{
					"type":    "NOT_FOUND",
					"message": "Could not resolve to a Repository with the name 'me/secret-repo'.",
				},
			},
		})
	}))
	t.Cleanup(server.Close)
	GraphQLURL = server.URL

	client := NewClient("me", "x")
	_, err := client.GraphQL(context.Background(), "{viewer{login}}")
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), "secret-repo") {
		t.Fatalf("error names the repo: %v", err)
	}
	if !strings.Contains(err.Error(), "NOT_FOUND") {
		t.Fatalf("expected the error type to survive: %v", err)
	}
}

func TestPublicStarsSkipPrivateRepos(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]any{
			map[string]any{"full_name": "me/public-one", "stargazers_count": 5.0, "private": false},
			map[string]any{"full_name": "me/secret-repo", "stargazers_count": 1.0, "private": true},
		})
	}))
	t.Cleanup(server.Close)
	RESTURL = server.URL

	stats := NewStats("me", NewClient("me", "x"))
	stars, err := stats.publicStars(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, found := stars["me/secret-repo"]; found {
		t.Fatal("private repo landed in the star counts")
	}
	if stars["me/public-one"] != 5 {
		t.Fatalf("public repo missing: %v", stars)
	}
}

func TestSafeErrDropsPanicValue(t *testing.T) {
	err := safeErr(func() error {
		panic("https://api.github.com/repos/me/secret-repo")
	})
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), "secret-repo") {
		t.Fatalf("panic value leaked: %v", err)
	}
}

func TestSafeCallPassesResult(t *testing.T) {
	value, err := safeCall(func() (any, error) { return 42, nil })
	if err != nil || value != 42 {
		t.Fatalf("got %v, %v", value, err)
	}
}

func TestStdlibLogSilenced(t *testing.T) {
	silenceStdlibLog()
	if log.Writer() != io.Discard {
		t.Fatalf("stdlib logger still writes somewhere: %T", log.Writer())
	}
}

func TestRecoverPanicReturnsError(t *testing.T) {
	var err error
	func() {
		defer recoverPanic(&err)
		panic("boom")
	}()
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), "boom") {
		t.Fatalf("panic value leaked: %v", err)
	}
}

func TestGraphQLErrorWithoutSlashKeepsText(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"errors": []any{map[string]any{
				"type":    "FORBIDDEN",
				"message": "Resource not accessible by personal access token",
			}},
		})
	}))
	t.Cleanup(server.Close)
	GraphQLURL = server.URL

	_, err := NewClient("me", "x").GraphQL(context.Background(), "{viewer{login}}")
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "Resource not accessible") {
		t.Fatalf("the useful message was dropped: %v", err)
	}
}
