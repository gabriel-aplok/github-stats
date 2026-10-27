package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"runtime/debug"
	"sync"
)

var stderr = os.Stderr

func main() {
	// the stdlib logger can print urls, we do not use it
	silenceStdlibLog()

	if err := run(os.Args[1:]); err != nil {
		var queriesErr *QueriesError
		if errors.As(err, &queriesErr) {
			fmt.Fprintf(os.Stderr, "error: %s\n", queriesErr.Message)
		} else {
			fmt.Fprintf(os.Stderr, "error: %s\n", err)
		}
		os.Exit(1)
	}
}

func silenceStdlibLog() {
	log.SetOutput(io.Discard)
}

// keeps a stack trace out of the log unless VERBOSE is set
func recoverPanic(err *error) {
	if recover() == nil {
		return
	}
	if os.Getenv("VERBOSE") != "" {
		debug.PrintStack()
	}
	*err = errors.New("unexpected failure")
}

// panic values are dropped, they may hold a url
var errUnexpected = errors.New("unexpected failure")

func safeErr(fn func() error) (err error) {
	defer func() {
		if recover() != nil {
			err = errUnexpected
		}
	}()
	return fn()
}

func safeCall(fn func() (any, error)) (any, error) {
	var value any
	err := safeErr(func() error {
		var inner error
		value, inner = fn()
		return inner
	})
	if err != nil {
		return nil, err
	}
	return value, nil
}

func run(args []string) (err error) {
	defer recoverPanic(&err)

	ctx := context.Background()
	return runImages(ctx)
}

func runImages(ctx context.Context) error {
	config, err := LoadConfig()
	if err != nil {
		return err
	}

	client := NewClient(config.User, config.AccessToken)
	stats := NewStats(config.User, client)
	stats.IgnoreForkedRepos = config.IgnoreForkedRepos
	stats.ExcludeRepos = config.ExcludeRepos
	stats.ExcludeLangs = config.ExcludeLangs

	if err := stats.Load(ctx); err != nil {
		return err
	}
	// independent, so run them together, cards after
	var wg sync.WaitGroup
	errs := make([]error, 3)
	wg.Add(3)
	go func() { defer wg.Done(); errs[0] = safeErr(func() error { return stats.LoadContributions(ctx) }) }()
	go func() { defer wg.Done(); errs[1] = safeErr(func() error { return stats.LoadLinesChanged(ctx) }) }()
	go func() { defer wg.Done(); errs[2] = safeErr(func() error { return stats.LoadViews(ctx) }) }()
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return err
		}
	}

	for _, render := range []func(*Stats) error{GenerateLanguages, GenerateOverview} {
		if err := render(stats); err != nil {
			return err
		}
	}
	if err := WriteStatsJSON(stats, config.StatsFile); err != nil {
		return err
	}

	if os.Getenv("VERBOSE") != "" {
		fmt.Println(stats.String())
	}
	return nil
}
