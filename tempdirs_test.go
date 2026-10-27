package main

import (
	"os"
	"path/filepath"
	"testing"
)

// isolates the tests from the working tree, so a run never writes into
// the repo and hand-edited cards stay untouched
func useTempDirs(t *testing.T) {
	t.Helper()
	root := t.TempDir()

	beforeTemplates, beforeGenerated := templatesDir, generatedDir
	templatesDir = filepath.Join(root, "templates")
	generatedDir = filepath.Join(root, "generated")
	t.Cleanup(func() {
		templatesDir, generatedDir = beforeTemplates, beforeGenerated
	})

	if err := os.MkdirAll(templatesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"overview.svg", "languages.svg"} {
		raw, err := os.ReadFile(filepath.Join(beforeTemplates, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(templatesDir, name), raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
