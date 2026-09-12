package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// doctier is a standalone tool: it must stay usable in any repository by users
// who have never heard of any particular docs tree or workflow plugin. The
// manifest is the only source of layout knowledge, so the code itself must not
// mention specific document trees. This guard fails the build if that coupling
// ever creeps in.
var forbiddenLayoutReferences = []string{
	".harness/",
	"engineering/features/",
	"engineering/architecture.md",
	"implementation-plan.md",
	"product/roadmap.md",
	"qa/report.md",
}

func TestCodeDoesNotReferenceSpecificDocsTrees(t *testing.T) {
	err := filepath.Walk("..", func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			// Skip vendored/hidden trees; cmd and internal hold the package code.
			if name := info.Name(); strings.HasPrefix(name, ".") || name == "node_modules" || name == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, forbidden := range forbiddenLayoutReferences {
			if strings.Contains(string(data), forbidden) {
				t.Errorf("%s references %q: doctier must not know about any specific docs tree; layout belongs in the user's manifest", path, forbidden)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
