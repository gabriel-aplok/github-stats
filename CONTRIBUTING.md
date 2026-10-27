# contributing

Issues and prs go to
[gabriel-aplok/github-stats](https://github.com/gabriel-aplok/github-stats).

## setup

Go 1.27 or newer. Nothing to download.

```sh
go build -o github-stats .
```

## before sending a patch

```sh
gofmt -l .
go vet ./...
go test ./...
```

## testing without a token

`GraphQLURL` and `RESTURL` are package variables. Point them at a local
`httptest` server and the code talks to that instead, so no test touches the
real api. `hostile_test.go` feeds wrong types and empty bodies through every
field to catch panics and zeroed cards.

## style

Short functions, plain names, no dead code. A comment should say why, not what.
Leave generated files out of pull requests, the workflow rewrites them.
