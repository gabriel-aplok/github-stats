package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// tests swap these
var (
	GraphQLURL = "https://api.github.com/graphql"
	RESTURL    = "https://api.github.com"
)

const (
	userAgent = "github-stats"

	concurrentRequests = 10

	graphqlAttempts = 5

	// github answers 202 until ready, so bound the wait
	contributorStatsPath     = "/repos/%s/stats/contributors"
	contributorStatsRetries  = 5
	contributorStatsAttempts = 3

	viewsPath    = "/repos/%s/traffic/views"
	viewsRetries = 5
)

// var so tests can drop the wait
var retryBaseDelay = 2 * time.Second

type QueriesError struct{ Message string }

func (e *QueriesError) Error() string { return e.Message }

func queriesErrorf(format string, args ...any) error {
	return &QueriesError{Message: fmt.Sprintf(format, args...)}
}

type Client struct {
	user    string
	token   string
	http    *http.Client
	slot    chan struct{}
	warnMu  sync.Mutex
	warned  map[string]bool
	headers map[string]string
}

func NewClient(user, token string) *Client {
	return &Client{
		user:   user,
		token:  token,
		http:   &http.Client{Timeout: 60 * time.Second},
		slot:   make(chan struct{}, concurrentRequests),
		warned: map[string]bool{},
		headers: map[string]string{
			"Authorization": "Bearer " + token,
			"User-Agent":    userAgent,
			"Accept":        "application/vnd.github+json",
		},
	}
}

func (c *Client) acquire(ctx context.Context) error {
	select {
	case c.slot <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *Client) release() { <-c.slot }

// every failure here is rebuilt, the raw error carries the url
func (c *Client) do(ctx context.Context, method, target string, body io.Reader) (int, []byte, error) {
	if err := c.acquire(ctx); err != nil {
		return 0, nil, err
	}
	defer c.release()

	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return 0, nil, queriesErrorf("could not build the request: %s", transportReason(err))
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, nil, queriesErrorf("request to github failed: %s", transportReason(err))
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, queriesErrorf("reading the reply failed: %s", transportReason(err))
	}
	return resp.StatusCode, payload, nil
}

func (c *Client) GraphQL(ctx context.Context, query string) (map[string]any, error) {
	request, err := json.Marshal(map[string]string{"query": query})
	if err != nil {
		return nil, err
	}

	var lastStatus int
	for attempt := range graphqlAttempts {
		status, body, err := c.do(ctx, http.MethodPost, GraphQLURL, bytes.NewReader(request))
		if err != nil {
			return nil, err
		}
		lastStatus = status
		if status == http.StatusUnauthorized {
			return nil, queriesErrorf(
				"GitHub rejected the token (HTTP 401). " +
					"Check that ACCESS_TOKEN is valid and not expired.",
			)
		}

		// rate limit replies can be html or empty
		var result map[string]any
		parseErr := json.Unmarshal(body, &result)
		if parseErr == nil && result != nil {
			if _, ok := result["data"]; !ok && result["errors"] == nil {
				// a reply with neither is broken, not an empty account
				return nil, queriesErrorf("graphql reply had no data")
			}
			return c.checkGraphQLErrors(result)
		}

		retryable := status == http.StatusOK || status == http.StatusForbidden ||
			status == http.StatusTooManyRequests || status >= 500
		if !retryable || attempt == graphqlAttempts-1 {
			return nil, queriesErrorf(
				"GraphQL request failed (HTTP %d) after %d attempts",
				status, attempt+1,
			)
		}
		if err := sleep(ctx, retryBaseDelay*time.Duration(attempt+1)); err != nil {
			return nil, err
		}
	}
	return nil, queriesErrorf("GraphQL request failed (HTTP %d)", lastStatus)
}

func (c *Client) checkGraphQLErrors(result map[string]any) (map[string]any, error) {
	rawErrors, _ := result["errors"].([]any)
	if len(rawErrors) == 0 {
		return result, nil
	}

	seen := map[string]bool{}
	var messages []string
	for _, raw := range rawErrors {
		entry, _ := raw.(map[string]any)
		message := safeMessage(entry)
		if !seen[message] {
			seen[message] = true
			messages = append(messages, message)
		}
	}
	joined := strings.Join(messages, ", ")

	// partial errors are normal, fail only when no data came back
	if result["data"] == nil {
		return nil, queriesErrorf("GraphQL query failed: %s", joined)
	}

	c.warnMu.Lock()
	defer c.warnMu.Unlock()
	c.warned[joined] = true
	if len(c.warned) == 1 {
		warnf(
			"graphql refused some fields (%s). repos the token cannot read are "+
				"left out, so the totals may be low. grant it read access to fix that.",
			joined,
		)
	}
	return result, nil
}

// REST returns the body, nil on a non 200
func (c *Client) REST(ctx context.Context, path string, params url.Values, retries int) (any, error) {
	target := RESTURL + "/" + strings.TrimPrefix(path, "/")
	if len(params) > 0 {
		target += "?" + params.Encode()
	}

	for range retries {
		status, body, err := c.do(ctx, http.MethodGet, target, nil)
		if err != nil {
			return nil, err
		}
		switch status {
		case http.StatusOK:
			var result any
			if err := json.Unmarshal(body, &result); err != nil {
				return nil, nil
			}
			return result, nil
		case http.StatusAccepted:
			if err := sleep(ctx, retryBaseDelay); err != nil {
				return nil, err
			}
		default:
			return nil, nil
		}
	}
	return nil, nil
}

func sleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func warnf(format string, args ...any) {
	fmt.Fprintf(stderr, "warning: "+format+"\n", args...)
}

// safeMessage drops messages with a slash, they can name a repo
func safeMessage(entry map[string]any) string {
	kind, _ := entry["type"].(string)
	if kind == "" {
		kind = "error"
	}
	message, _ := entry["message"].(string)
	if message == "" || strings.Contains(message, "/") {
		return kind
	}
	return message
}

// transportReason keeps the failure kind, drops the url
func transportReason(err error) string {
	var netErr net.Error
	var opErr *net.OpError
	var dnsErr *net.DNSError
	var sysErr *os.SyscallError
	switch {
	case errors.Is(err, context.Canceled):
		return "cancelled"
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &netErr) && netErr.Timeout():
		return "timeout"
	case errors.As(err, &dnsErr):
		return "dns lookup failed"
	case errors.As(err, &opErr):
		if errors.As(opErr.Err, &sysErr) {
			return opErr.Op + ": " + sysErr.Err.Error()
		}
		return opErr.Op + " failed"
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return transportReason(urlErr.Err)
	}
	return fmt.Sprintf("%T", err)
}

/* query builders */

func gqlCursor(cursor string) string {
	if cursor == "" {
		return "null"
	}
	return `"` + strings.ReplaceAll(cursor, `"`, "") + `"`
}

func repoFields(pad string, includeStars bool) string {
	pad2 := pad + "  "
	var b strings.Builder
	fmt.Fprintf(&b, "%snodes {\n", pad)
	fmt.Fprintf(&b, "%snameWithOwner\n", pad2)
	fmt.Fprintf(&b, "%sforkCount\n", pad2)
	fmt.Fprintf(&b, "%slanguages(first: 10, orderBy: {field: SIZE, direction: DESC}) {\n", pad2)
	fmt.Fprintf(&b, "%s  edges {\n", pad2)
	fmt.Fprintf(&b, "%s    size\n", pad2)
	fmt.Fprintf(&b, "%s    node {\n", pad2)
	fmt.Fprintf(&b, "%s      name\n", pad2)
	fmt.Fprintf(&b, "%s      color\n", pad2)
	fmt.Fprintf(&b, "%s    }\n", pad2)
	fmt.Fprintf(&b, "%s  }\n", pad2)
	fmt.Fprintf(&b, "%s}\n", pad2)
	if includeStars {
		fmt.Fprintf(&b, "%sstargazers {\n", pad2)
		fmt.Fprintf(&b, "%s  totalCount\n", pad2)
		fmt.Fprintf(&b, "%s}\n", pad2)
	}
	fmt.Fprintf(&b, "%s}\n", pad)
	return b.String()
}

func ReposOverview(contribCursor, ownedCursor string, includeStars bool) string {
	nodes := strings.TrimSuffix(repoFields("      ", includeStars), "\n")
	return fmt.Sprintf(`{
  viewer {
    login,
    name,
    repositories(
        first: 100,
        orderBy: {
            field: UPDATED_AT,
            direction: DESC
        },
        isFork: false,
        after: %s
    ) {
      pageInfo {
        hasNextPage
        endCursor
      }
%s
    }
    repositoriesContributedTo(
        first: 100,
        includeUserRepositories: false,
        orderBy: {
            field: UPDATED_AT,
            direction: DESC
        },
        contributionTypes: [
            COMMIT,
            PULL_REQUEST,
            REPOSITORY,
            PULL_REQUEST_REVIEW
        ]
        after: %s
    ) {
      pageInfo {
        hasNextPage
        endCursor
      }
%s
    }
  }
}
`, gqlCursor(ownedCursor), nodes, gqlCursor(contribCursor), nodes)
}

func ContribYears() string {
	return `
query {
  viewer {
    contributionsCollection {
      contributionYears
    }
  }
}
`
}

func contribsByYear(year int) string {
	return fmt.Sprintf(`
    year%d: contributionsCollection(
        from: "%d-01-01T00:00:00Z",
        to: "%d-01-01T00:00:00Z"
    ) {
      contributionCalendar {
        totalContributions
      }
    }
`, year, year, year+1)
}

func AllContribs(years []int) string {
	var b strings.Builder
	for _, year := range years {
		b.WriteString(contribsByYear(year))
	}
	return fmt.Sprintf(`
query {
  viewer {
    %s
  }
}
`, b.String())
}

/* json helpers */

func asMap(value any) map[string]any {
	m, _ := value.(map[string]any)
	return m
}

func asSlice(value any) []any {
	s, _ := value.([]any)
	return s
}

func asString(value any) string {
	s, _ := value.(string)
	return s
}

func asInt(value any) int {
	f, _ := value.(float64)
	return int(f)
}

func asBool(value any) bool {
	b, _ := value.(bool)
	return b
}

func viewerPath(result map[string]any) map[string]any {
	return asMap(asMap(asMap(result)["data"])["viewer"])
}

func itoa(i int) string { return strconv.Itoa(i) }
