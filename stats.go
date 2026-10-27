package main

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultName = "GitHub user"

	// below this share, drop the language
	shareFloor = 0.005

	contributorReposParallel = 10

	starPageSize = 100
	maxStarPages = 100
)

type Language struct {
	Size  int
	Color string
	Share float64
}

type LinesChanged struct{ Added, Removed int }

type Stats struct {
	User              string
	Queries           *Client
	IgnoreForkedRepos bool
	ExcludeRepos      map[string]bool
	ExcludeLangs      map[string]bool

	once               sync.Once
	loadErr            error
	Name               string
	Stargazers         int
	Forks              int
	TotalContributions int
	Languages          map[string]*Language
	Repos              map[string]bool
	Lines              LinesChanged
	Views              int
}

func NewStats(user string, client *Client) *Stats {
	return &Stats{
		User:         user,
		Queries:      client,
		ExcludeRepos: map[string]bool{},
		ExcludeLangs: map[string]bool{},
	}
}

func (s *Stats) Load(ctx context.Context) error {
	s.once.Do(func() { s.loadErr = s.load(ctx) })
	return s.loadErr
}

func (s *Stats) load(ctx context.Context) error {
	s.Stargazers, s.Forks = 0, 0
	s.Languages = map[string]*Language{}
	s.Repos = map[string]bool{}

	excludeLangs := map[string]bool{}
	for name := range s.ExcludeLangs {
		excludeLangs[strings.ToLower(name)] = true
	}

	// separate query, unread stargazers null the whole page
	starCounts, err := s.stargazersByRepo(ctx)
	if err != nil {
		return err
	}
	if len(starCounts) == 0 {
		// token cannot read stargazers, use the public counts instead
		starCounts, err = s.publicStars(ctx)
		if err != nil {
			return err
		}
	}

	nextOwned, nextContrib := "", ""
	for {
		result, err := s.Queries.GraphQL(ctx, ReposOverview(nextContrib, nextOwned, false))
		if err != nil {
			return err
		}
		viewer := viewerPath(result)
		if viewer == nil {
			// writing a zeroed card over a good one is worse than failing
			return queriesErrorf("graphql reply had no viewer")
		}

		s.Name = asString(viewer["name"])
		if s.Name == "" {
			s.Name = asString(viewer["login"])
		}
		if s.Name == "" {
			s.Name = "No Name"
		}

		contribRepos := asMap(viewer["repositoriesContributedTo"])
		ownedRepos := asMap(viewer["repositories"])

		repos := append([]any{}, asSlice(ownedRepos["nodes"])...)
		if !s.IgnoreForkedRepos {
			repos = append(repos, asSlice(contribRepos["nodes"])...)
		}

		for _, raw := range repos {
			repo := asMap(raw)
			if repo == nil {
				continue
			}
			name := asString(repo["nameWithOwner"])
			if name == "" || s.Repos[name] || s.ExcludeRepos[name] {
				continue
			}
			s.Repos[name] = true

			s.Stargazers += starCounts[name]
			s.Forks += asInt(repo["forkCount"])

			for _, rawEdge := range asSlice(asMap(repo["languages"])["edges"]) {
				edge := asMap(rawEdge)
				node := asMap(edge["node"])
				lang := asString(node["name"])
				if lang == "" {
					lang = "Other"
				}
				if excludeLangs[strings.ToLower(lang)] {
					continue
				}
				entry, ok := s.Languages[lang]
				if !ok {
					entry = &Language{Color: asString(node["color"])}
					s.Languages[lang] = entry
				}
				entry.Size += asInt(edge["size"])
			}
		}

		ownedInfo := asMap(ownedRepos["pageInfo"])
		contribInfo := asMap(contribRepos["pageInfo"])
		if !asBool(ownedInfo["hasNextPage"]) && !asBool(contribInfo["hasNextPage"]) {
			break
		}
		beforeOwned, beforeContrib := nextOwned, nextContrib
		if cursor := asString(ownedInfo["endCursor"]); cursor != "" {
			nextOwned = cursor
		}
		if cursor := asString(contribInfo["endCursor"]); cursor != "" {
			nextContrib = cursor
		}
		if nextOwned == beforeOwned && nextContrib == beforeContrib {
			// the cursor stopped moving, another round would repeat this page
			warnf("pagination stuck, stopping early")
			break
		}
	}

	total := 0
	for _, lang := range s.Languages {
		total += lang.Size
	}
	for _, lang := range s.Languages {
		if total > 0 {
			lang.Share = float64(lang.Size) / float64(total)
		}
	}
	return nil
}

func (s *Stats) stargazersByRepo(ctx context.Context) (map[string]int, error) {
	counts := map[string]int{}
	seen := map[string]bool{}
	cursor := ""
	for {
		result, err := s.Queries.GraphQL(ctx, ReposOverview("", cursor, true))
		if err != nil {
			return nil, err
		}
		repositories := asMap(viewerPath(result)["repositories"])
		nodes := asSlice(repositories["nodes"])

		usable := 0
		for _, raw := range nodes {
			repo := asMap(raw)
			name := asString(repo["nameWithOwner"])
			if name == "" {
				continue
			}
			usable++
			stars := asInt(asMap(repo["stargazers"])["totalCount"])
			counts[name] = stars
			seen[name] = true
		}

		pageInfo := asMap(repositories["pageInfo"])
		next := asString(pageInfo["endCursor"])
		if len(nodes) == 0 || !asBool(pageInfo["hasNextPage"]) || seen[next] {
			break
		}
		if usable == 0 {
			// nothing usable, a cursor returns the same nulls
			break
		}
		seen[next] = true
		cursor = next
	}
	return counts, nil
}

func (s *Stats) publicStars(ctx context.Context) (map[string]int, error) {
	stars := map[string]int{}
	for page := 1; page <= maxStarPages; page++ {
		params := url.Values{}
		params.Set("per_page", itoa(starPageSize))
		params.Set("page", itoa(page))
		params.Set("type", "owner")

		value, err := s.Queries.REST(ctx, "/users/"+s.User+"/repos", params, 1)
		if err != nil {
			return nil, err
		}
		repos, ok := value.([]any)
		if !ok || len(repos) == 0 {
			break
		}
		for _, raw := range repos {
			repo := asMap(raw)
			if asBool(repo["private"]) {
				continue
			}
			if name := asString(repo["full_name"]); name != "" {
				stars[name] = asInt(repo["stargazers_count"])
			}
		}
		if len(repos) < starPageSize {
			break
		}
	}
	return stars, nil
}

func (s *Stats) LoadContributions(ctx context.Context) error {
	result, err := s.Queries.GraphQL(ctx, ContribYears())
	if err != nil {
		return err
	}
	var years []int
	for _, raw := range asSlice(asMap(viewerPath(result)["contributionsCollection"])["contributionYears"]) {
		years = append(years, asInt(raw))
	}
	sort.Ints(years)

	total := 0
	if len(years) > 0 {
		result, err := s.Queries.GraphQL(ctx, AllContribs(years))
		if err != nil {
			return err
		}
		for _, raw := range asMap(viewerPath(result)) {
			total += asInt(asMap(asMap(raw)["contributionCalendar"])["totalContributions"])
		}
	}
	s.TotalContributions = total
	return nil
}

func (s *Stats) LoadLinesChanged(ctx context.Context) error {
	repos := s.RepoNames()
	results := make([]any, len(repos))

	var wg sync.WaitGroup
	var mu sync.Mutex
	failures := 0
	slots := make(chan struct{}, contributorReposParallel)
	for i, repo := range repos {
		wg.Add(1)
		go func() {
			defer wg.Done()
			slots <- struct{}{}
			defer func() { <-slots }()

			path := fmt.Sprintf(contributorStatsPath, repo)
			value, err := safeCall(func() (any, error) {
				return s.contributorStats(ctx, path)
			})
			mu.Lock()
			if err != nil {
				failures++
			}
			results[i] = value
			mu.Unlock()
		}()
	}
	wg.Wait()

	added, removed, skipped := 0, 0, 0
	for _, raw := range results {
		list, ok := raw.([]any)
		if !ok {
			skipped++
			continue
		}
		for _, rawAuthor := range list {
			authorObj := asMap(rawAuthor)
			author := asMap(authorObj["author"])
			if author == nil || asString(author["login"]) != s.User {
				continue
			}
			for _, rawWeek := range asSlice(authorObj["weeks"]) {
				week := asMap(rawWeek)
				added += asInt(week["a"])
				removed += asInt(week["d"])
			}
		}
	}

	if skipped > 0 || failures > 0 {
		warnf(
			"contributor stats missing for %d of %d repos, lines changed may be low",
			skipped+failures, len(repos),
		)
	}
	s.Lines = LinesChanged{Added: added, Removed: removed}
	return nil
}

func (s *Stats) contributorStats(ctx context.Context, path string) (any, error) {
	for attempt := 0; attempt < contributorStatsAttempts; attempt++ {
		value, err := s.Queries.REST(ctx, path, nil, contributorStatsRetries)
		if err != nil {
			return nil, err
		}
		if value != nil {
			return value, nil
		}
		if attempt+1 < contributorStatsAttempts {
			if err := sleep(ctx, retryBaseDelay*time.Duration(attempt+1)); err != nil {
				return nil, err
			}
		}
	}
	return nil, nil
}

// github keeps 14 days only
func (s *Stats) LoadViews(ctx context.Context) error {
	repos := s.RepoNames()
	results := make([]any, len(repos))

	var wg sync.WaitGroup
	var mu sync.Mutex
	failures := 0
	slots := make(chan struct{}, contributorReposParallel)
	for i, repo := range repos {
		wg.Add(1)
		go func() {
			defer wg.Done()
			slots <- struct{}{}
			defer func() { <-slots }()

			value, err := safeCall(func() (any, error) {
				return s.Queries.REST(ctx, fmt.Sprintf(viewsPath, repo), nil, viewsRetries)
			})
			mu.Lock()
			if err != nil {
				failures++
			}
			results[i] = value
			mu.Unlock()
		}()
	}
	wg.Wait()

	total := 0
	for _, raw := range results {
		for _, rawView := range asSlice(asMap(raw)["views"]) {
			total += asInt(asMap(rawView)["count"])
		}
	}
	if failures > 0 {
		warnf(
			"views missing for %d of %d repos, the total is low",
			failures, len(repos),
		)
	}
	s.Views = total
	return nil
}

func (s *Stats) RepoNames() []string {
	names := make([]string, 0, len(s.Repos))
	for name := range s.Repos {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (s *Stats) ProportionalLanguages() []namedShare {
	shares := make([]namedShare, 0, len(s.Languages))
	for name, lang := range s.Languages {
		shares = append(shares, namedShare{Name: name, Size: lang.Size, Share: lang.Share})
	}
	sort.SliceStable(shares, func(i, j int) bool { return shares[i].Size > shares[j].Size })
	return shares
}

type namedShare struct {
	Name  string
	Size  int
	Share float64
}

func (s *Stats) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Name: %s\n", s.Name)
	fmt.Fprintf(&b, "Stargazers: %s\n", groupDigits(s.Stargazers))
	fmt.Fprintf(&b, "Forks: %s\n", groupDigits(s.Forks))
	fmt.Fprintf(&b, "All-time contributions: %s\n", groupDigits(s.TotalContributions))
	fmt.Fprintf(&b, "Repositories with contributions: %d\n", len(s.Repos))
	fmt.Fprintf(&b, "Lines of code added: %s\n", groupDigits(s.Lines.Added))
	fmt.Fprintf(&b, "Lines of code deleted: %s\n", groupDigits(s.Lines.Removed))
	fmt.Fprintf(&b, "Lines of code changed: %s\n", groupDigits(s.Lines.Added+s.Lines.Removed))
	fmt.Fprintf(&b, "Project page views: %s\n", groupDigits(s.Views))
	b.WriteString("Languages:")
	for _, share := range s.ProportionalLanguages() {
		fmt.Fprintf(&b, "\n  - %s: %.4f%%", share.Name, 100*share.Share)
	}
	return b.String()
}

func groupDigits(value int) string {
	digits := strconv.Itoa(value)
	sign := ""
	if strings.HasPrefix(digits, "-") {
		sign, digits = "-", digits[1:]
	}
	var b strings.Builder
	for i, r := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	return sign + b.String()
}
