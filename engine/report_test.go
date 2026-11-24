package engine_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/CodMac/arch-lens-dep-analyzer/core"
	"github.com/CodMac/arch-lens-dep-analyzer/engine"
	_ "github.com/CodMac/arch-lens-dep-analyzer/x/cpp"
)

func runWithMissingFile(t *testing.T, bestEffort bool) (*engine.EngineContext, error) {
	t.Helper()
	dir := t.TempDir()
	good := filepath.Join(dir, "good.cpp")
	if err := os.WriteFile(good, []byte("int f() { return 0; }\n"), 0644); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "missing.cpp")

	impl, err := core.Get(core.Language("cpp"))
	if err != nil {
		t.Fatal(err)
	}
	return engine.RunDefault(engine.Config{
		Language:    core.Language("cpp"),
		RootPath:    dir,
		FilePaths:   []string{good, missing},
		Concurrency: 2,
		FilterLevel: core.LevelRaw,
		BestEffort:  bestEffort,
	}, func() (core.Parser, error) {
		return impl.Parser.NewParser(), nil
	})
}

func TestBestEffortCollector(t *testing.T) {
	ec, err := runWithMissingFile(t, true)
	if err != nil {
		t.Fatalf("best-effort run should not fail: %v", err)
	}

	var ok, bad int
	for _, o := range ec.FileOutcomes() {
		switch o.Path {
		case "good.cpp":
			ok++
			if o.Error != "" {
				t.Errorf("good.cpp should succeed, got %q", o.Error)
			}
			if o.Elements == 0 {
				t.Errorf("good.cpp should produce elements")
			}
		case "missing.cpp":
			bad++
			if o.Stage != "collector" || o.Error == "" {
				t.Errorf("missing.cpp outcome = %+v", o)
			}
		}
	}
	if ok != 1 || bad != 1 {
		t.Errorf("outcomes ok=%d bad=%d, want 1/1", ok, bad)
	}
	if len(ec.StageTimings) == 0 {
		t.Errorf("stage timings should be recorded")
	}
}

func TestStrictCollectorFails(t *testing.T) {
	if _, err := runWithMissingFile(t, false); err == nil {
		t.Fatal("strict run should fail on missing file")
	}
}
