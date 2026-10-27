# github stats

<img src="https://github.com/gabriel-aplok/github-stats/blob/main/generated/overview.svg#gh-dark-mode-only" />
<img src="https://github.com/gabriel-aplok/github-stats/blob/main/generated/languages.svg#gh-dark-mode-only" />
<img src="https://github.com/gabriel-aplok/github-stats/blob/main/generated/overview.svg#gh-light-mode-only" />
<img src="https://github.com/gabriel-aplok/github-stats/blob/main/generated/languages.svg#gh-light-mode-only" />

Two svg cards for your profile readme. The first one shows stars, forks,
contributions, lines changed and repo views. The second shows your languages by
file size. Both follow the github light and dark theme by themselves.

A workflow rebuilds them every morning. The tool is one go binary with no
dependencies, so the job only compiles and runs it.

Started as a port of [jstrieb/github-stats](https://github.com/jstrieb/github-stats),
which was python.

## why bother

Your profile only shows stars and pinned repos. If most of your work is private,
the profile makes you look idle. Github also counts contributions for the current
year only.

The cards count private repos, repos you contributed to and every year you were
active. The api calls run with your token inside your own runner, so nothing
private leaves github.

## run it locally

You need go 1.27 or newer.

```sh
go build -o github-stats .
ACCESS_TOKEN=... GITHUB_USER=your-username ./github-stats
```

Files land in `generated/`. `GITHUB_USER` exists because `GITHUB_ACTOR` is only
set inside actions. Without a user the tool has nobody to look up.

`VERBOSE=1` also prints every number as plain text.

Optional variables:

| variable | what it does |
| --- | --- |
| `EXCLUDED` | `owner/name` repos to skip, comma separated |
| `EXCLUDED_LANGS` | languages to skip, like `html,tex` |
| `EXCLUDE_FORKED_REPOS` | `true` counts owned repos only |
| `STATS_JSON` | file name under `generated/` for a json dump |

## what the token needs

The graphql api is all or nothing per repo. If the token cannot read one field,
every field of that repo comes back null and the repo vanishes from the totals,
languages included. Give the token read access to every repo you want counted.

Fine-grained token: Metadata, Contents and Administration (for traffic views) on
the repos you pick, plus Profile under account permissions.

Classic token: `read:user` and `repo`.

Stars on repos you do not own are the usual exception. When graphql refuses the
stargazer field, the tool asks the public rest api instead. No extra permission
and no extra setup. Private repos lose their star count in that case, since the
public api cannot see them.

## setup

1. Make the token above. Fine-grained is better, classic still works.
2. Copy this repo with the
   [template](https://github.com/jstrieb/github-stats/generate) instead of a fork,
   so the old commit history does not come along.
3. Settings, Secrets and variables, Actions. Add the `ACCESS_TOKEN` secret.
4. Optional settings go on the Variables tab. Variables are public, so put a
   value in a secret with the same name if it mentions a private repo.
5. Run the Generate Stats Images workflow once from the Actions tab.
6. Paste the card urls into your profile readme:
   ```md
   ![](https://raw.githubusercontent.com/USER/github-stats/main/generated/overview.svg#gh-dark-mode-only)
   ![](https://raw.githubusercontent.com/USER/github-stats/main/generated/overview.svg#gh-light-mode-only)
   ![](https://raw.githubusercontent.com/USER/github-stats/main/generated/languages.svg#gh-dark-mode-only)
   ![](https://raw.githubusercontent.com/USER/github-stats/main/generated/languages.svg#gh-light-mode-only)
   ```

## known inaccuracies

Views and lines changed come from github and they are rough. Views cover 14 days
only. Contributors stats are built on demand, github answers `202 Accepted`
until they exist, and repos you last touched over a year ago sometimes never show
up. The tool waits a limited time and tells you how many repos it skipped.

## leaks

Your cards need a public repo so the profile can load them. That does not mean
private repo names have to appear in the logs or in the files.

Six paths could leak a name. All six are closed.

| path | what it did | fix |
| --- | --- | --- |
| http errors | `url.Error` printed the full request url | only the failure kind is kept |
| request building | `url.Parse` errors embed the url too | same treatment |
| body read errors | a transport error can resurface there | same treatment |
| graphql errors | github can write `owner/name` into the message | a message with a slash falls back to the error type |
| panic | a stack trace names paths, and the value may hold a url | recovered, value dropped, one short line |
| repo names on disk | a star-count file used to be committed | removed, stars come from rest at runtime |

The standard library logger is sent to `io.Discard` at startup. Go's http2
transport writes urls to it on some failures, and nothing you need goes there.

`leak_test.go` covers each case and asserts the name is absent, so a regression
fails the suite instead of leaking quietly.

What still appears in the log, by design:

- counts such as `contributor stats missing for 11 of 153 repos`
- the display name and language names, and only with `VERBOSE=1`

One limit I cannot fix from inside the tool: a panic raised by the runtime
itself, such as a stack overflow or running out of memory, cannot be recovered
and prints a trace. Nil map writes are excluded by construction, since every map
is built before use, and `hostile_test.go` pushes wrong types through every
field to prove it.

The git history one is gone. Nothing with a repo name is written to disk any
more, so a repo that turns private later leaves no trace in the repo.

If a log line ever does name a repo, delete that workflow run. Nobody can read
it after deletion.

## contributing

Issues and prs go to
[gabriel-aplok/github-stats](https://github.com/gabriel-aplok/github-stats).
See `CONTRIBUTING.md` for the commands to run before sending a patch.

## license

GPL-3.0, same as the original. See [LICENSE](LICENSE).

Icons are [GitHub Octicons](https://primer.style/octicons/), so the cards match
the rest of github. The idea came from
[anuraghazra/github-readme-stats](https://github.com/anuraghazra/github-readme-stats).
