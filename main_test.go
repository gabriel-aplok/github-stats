package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fakeGraphQL(query string) map[string]any {
	switch {
	case strings.Contains(query, "contributionYears"):
		return map[string]any{"data": map[string]any{"viewer": map[string]any{
			"contributionsCollection": map[string]any{
				"contributionYears": []any{float64(2025), float64(2026)},
			},
		}}}
	case strings.Contains(query, "year2025"):
		return map[string]any{"data": map[string]any{"viewer": map[string]any{
			"year2025": map[string]any{"contributionCalendar": map[string]any{"totalContributions": float64(100)}},
			"year2026": map[string]any{"contributionCalendar": map[string]any{"totalContributions": float64(50)}},
		}}}
	case strings.Contains(query, "stargazers"):
		return map[string]any{"data": map[string]any{"viewer": map[string]any{
			"repositories": map[string]any{
				"pageInfo": map[string]any{"hasNextPage": false},
				"nodes": []any{map[string]any{
					"nameWithOwner": "me/repo",
					"stargazers":    map[string]any{"totalCount": float64(3)},
				}},
			},
		}}}
	}
	repo := map[string]any{
		"nameWithOwner": "me/repo",
		"forkCount":     float64(1),
		"languages": map[string]any{"edges": []any{
			map[string]any{"size": float64(100), "node": map[string]any{"name": "Python", "color": "#3572A5"}},
			map[string]any{"size": float64(50), "node": map[string]any{"name": "C<!>", "color": nil}},
			map[string]any{"size": float64(1), "node": map[string]any{"name": "Dust", "color": "#123456"}},
		}},
	}
	page := map[string]any{"pageInfo": map[string]any{"hasNextPage": false}}
	empty := map[string]any{"pageInfo": map[string]any{"hasNextPage": false}, "nodes": []any{}}
	return map[string]any{"data": map[string]any{"viewer": map[string]any{
		"login":                     "me",
		"name":                      "Me <&>",
		"repositories":              map[string]any{"pageInfo": page["pageInfo"], "nodes": []any{repo}},
		"repositoriesContributedTo": empty,
	}}}
}

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/graphql":
			var payload struct{ Query string }
			_ = json.NewDecoder(r.Body).Decode(&payload)
			_ = json.NewEncoder(w).Encode(fakeGraphQL(payload.Query))
		case strings.HasSuffix(r.URL.Path, "/stats/contributors"):
			_ = json.NewEncoder(w).Encode([]any{
				map[string]any{"author": map[string]any{"login": "me"},
					"weeks": []any{map[string]any{"a": float64(10), "d": float64(4)}}},
				map[string]any{"author": map[string]any{"login": "other"},
					"weeks": []any{map[string]any{"a": float64(99), "d": float64(99)}}},
			})
		case strings.HasSuffix(r.URL.Path, "/traffic/views"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"views": []any{map[string]any{"count": float64(7)}, map[string]any{"count": float64(5)}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func TestGenerateCards(t *testing.T) {
	server := newTestServer(t)
	GraphQLURL = server.URL + "/graphql"
	RESTURL = server.URL

	useTempDirs(t)

	t.Setenv("ACCESS_TOKEN", "x")
	t.Setenv("GITHUB_USER", "me")
	t.Setenv("EXCLUDED_LANGS", "dust")
	t.Setenv("STATS_JSON", "stats.json")

	if err := runImages(t.Context()); err != nil {
		t.Fatalf("runImages: %v", err)
	}
	overview := readFile(t, filepath.Join(generatedDir, "overview.svg"))
	for _, want := range []string{"Me &lt;&amp;&gt;", ">3<", ">150<", ">14<", ">12<"} {
		if !strings.Contains(overview, want) {
			t.Errorf("overview.svg missing %q", want)
		}
	}
	languages := readFile(t, filepath.Join(generatedDir, "languages.svg"))
	if strings.Contains(languages, "{{") {
		t.Error("languages.svg still has placeholders")
	}
	if !strings.Contains(languages, "C&lt;!&gt;") {
		t.Error("language name was not escaped")
	}
	if strings.Contains(languages, "Dust") {
		t.Error("excluded language still present")
	}
	if !strings.Contains(readFile(t, filepath.Join(generatedDir, "stats.json")), `"views": 12`) {
		t.Error("stats.json missing views")
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
