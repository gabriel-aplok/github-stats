package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"math"
	"os"
	"path/filepath"
	"strings"
)

const (
	maxLanguagesInBar    = 25
	maxLanguagesInLegend = 12

	legendDelayStep = 150
	fallbackColor   = "#8b949e"
)

var (
	templatesDir = "templates"
	generatedDir = "generated"
)

type Config struct {
	User              string
	AccessToken       string
	ExcludeRepos      map[string]bool
	ExcludeLangs      map[string]bool
	IgnoreForkedRepos bool
	StatsFile         string
}

func envSet(name string) map[string]bool {
	set := map[string]bool{}
	for _, part := range strings.Split(os.Getenv(name), ",") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			set[trimmed] = true
		}
	}
	return set
}

func envBool(name string) bool {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv(name)))
	return raw != "" && raw != "false" && raw != "0" && raw != "no"
}

func LoadConfig() (*Config, error) {
	token := os.Getenv("ACCESS_TOKEN")
	if token == "" {
		return nil, errors.New(
			"a personal access token is required to proceed: set ACCESS_TOKEN " +
				"to a token with the read:user scope, plus repo for private " +
				"repository stats and traffic metrics",
		)
	}
	user := firstNonEmpty(os.Getenv("GITHUB_ACTOR"), os.Getenv("GITHUB_USER"))
	if user == "" {
		return nil, errors.New(
			"environment variable GITHUB_ACTOR (or GITHUB_USER) must be set",
		)
	}
	return &Config{
		User:              user,
		AccessToken:       token,
		ExcludeRepos:      envSet("EXCLUDED"),
		ExcludeLangs:      envSet("EXCLUDED_LANGS"),
		IgnoreForkedRepos: envBool("EXCLUDE_FORKED_REPOS"),
		StatsFile:         os.Getenv("STATS_JSON"),
	}, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

// a value that looks like a placeholder is not replaced again
func Fill(template string, placeholders map[string]string) string {
	for key, value := range placeholders {
		template = strings.ReplaceAll(template, "{{ "+key+" }}", value)
	}
	return template
}

func renderName(name string) string {
	cleaned := strings.TrimSpace(name)
	if cleaned == "" {
		cleaned = defaultName
	}
	return html.EscapeString(cleaned)
}

func rowColor(color string) string {
	if color == "" {
		return fallbackColor
	}
	return color
}

func renderRows(shares []namedShare, colors map[string]string) (string, string) {
	var progress, legend strings.Builder
	shown := 0
	for _, share := range shares {
		if share.Share < shareFloor {
			continue
		}
		color := rowColor(colors[share.Name])
		if shown < maxLanguagesInBar {
			fmt.Fprintf(
				&progress,
				`<span style="background-color: %s;width: %.3f%%;" class="progress-item"></span>`+"\n",
				color, 100*share.Share,
			)
		}
		if shown < maxLanguagesInLegend {
			fmt.Fprintf(&legend, `
<li style="animation-delay: %dms;">
<svg xmlns="http://www.w3.org/2000/svg" class="octicon" style="fill:%s;"
viewBox="0 0 16 16" version="1.1" width="16" height="16"><path
fill-rule="evenodd" d="M8 4a4 4 0 100 8 4 4 0 000-8z"></path></svg>
<span class="lang">%s</span>
<span class="percent">%.2f%%</span>
</li>

`, shown*legendDelayStep, color, html.EscapeString(share.Name), 100*share.Share)
		}
		shown++
		if shown >= maxLanguagesInBar {
			break
		}
	}
	return strings.TrimSuffix(progress.String(), "\n"), legend.String()
}

func readTemplate(name string) (string, error) {
	raw, err := os.ReadFile(filepath.Join(templatesDir, name))
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func writeGenerated(name, content string) error {
	if err := os.MkdirAll(generatedDir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(generatedDir, name), []byte(content), 0o644)
}

func GenerateOverview(stats *Stats) error {
	template, err := readTemplate("overview.svg")
	if err != nil {
		return err
	}
	output := Fill(template, map[string]string{
		"name":          renderName(stats.Name),
		"stars":         groupDigits(stats.Stargazers),
		"forks":         groupDigits(stats.Forks),
		"contributions": groupDigits(stats.TotalContributions),
		"lines_changed": groupDigits(stats.Lines.Added + stats.Lines.Removed),
		"views":         groupDigits(stats.Views),
		"repos":         groupDigits(len(stats.Repos)),
	})
	return writeGenerated("overview.svg", output)
}

func GenerateLanguages(stats *Stats) error {
	template, err := readTemplate("languages.svg")
	if err != nil {
		return err
	}
	colors := map[string]string{}
	for name, lang := range stats.Languages {
		colors[name] = lang.Color
	}
	progress, legend := renderRows(stats.ProportionalLanguages(), colors)
	output := Fill(template, map[string]string{
		"progress":  progress,
		"lang_list": legend,
	})
	return writeGenerated("languages.svg", output)
}

func WriteStatsJSON(stats *Stats, fileName string) error {
	if fileName == "" {
		return nil
	}
	languages := map[string]float64{}
	total := 0
	for _, lang := range stats.Languages {
		total += lang.Size
	}
	if total == 0 {
		total = 1
	}
	for name, lang := range stats.Languages {
		languages[name] = round2(100 * float64(lang.Size) / float64(total))
	}
	snapshot := map[string]any{
		"name":                stats.Name,
		"stargazers":          stats.Stargazers,
		"forks":               stats.Forks,
		"total_contributions": stats.TotalContributions,
		"repositories":        len(stats.Repos),
		"lines_changed":       stats.Lines.Added + stats.Lines.Removed,
		"views":               stats.Views,
		"languages":           languages,
	}
	encoded, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return err
	}
	return writeGenerated(fileName, string(encoded)+"\n")
}

func round2(value float64) float64 {
	rounded := math.Floor(value*100+0.5) / 100
	// no -0 in the json
	if rounded == 0 {
		return 0
	}
	return rounded
}
