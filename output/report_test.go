package output

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CodMac/arch-lens-dep-analyzer/engine"
)

func readLines(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	text := strings.TrimSpace(string(data))
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

func TestExportFileList(t *testing.T) {
	dir := t.TempDir()
	e := NewExporter(dir)
	if err := e.ExportFileList(FileScanFiles, []string{"a.cpp", "b.cpp"}); err != nil {
		t.Fatal(err)
	}
	lines := readLines(t, filepath.Join(dir, FileScanFiles))
	if len(lines) != 2 {
		t.Fatalf("lines = %d, want 2", len(lines))
	}
	var rec filePathRecord
	if err := json.Unmarshal([]byte(lines[0]), &rec); err != nil {
		t.Fatal(err)
	}
	if rec.Path != "a.cpp" {
		t.Errorf("path = %q, want a.cpp", rec.Path)
	}
}

func TestExportFileOutcomes(t *testing.T) {
	dir := t.TempDir()
	e := NewExporter(dir)
	succeeded := []engine.FileOutcome{{Path: "ok.cpp", Elements: 3, Relations: 5, DurationMS: 7}}
	failed := []engine.FileOutcome{{Path: "bad.cpp", Stage: "collector", Error: "boom"}}
	if err := e.ExportFileOutcomes(succeeded, failed); err != nil {
		t.Fatal(err)
	}

	var got engine.FileOutcome
	if err := json.Unmarshal([]byte(readLines(t, filepath.Join(dir, FileScanSucceeded))[0]), &got); err != nil {
		t.Fatal(err)
	}
	if got.Path != "ok.cpp" || got.Elements != 3 || got.Relations != 5 || got.DurationMS != 7 {
		t.Errorf("succeeded outcome = %+v", got)
	}

	if err := json.Unmarshal([]byte(readLines(t, filepath.Join(dir, FileScanFailed))[0]), &got); err != nil {
		t.Fatal(err)
	}
	if got.Path != "bad.cpp" || got.Stage != "collector" || got.Error != "boom" {
		t.Errorf("failed outcome = %+v", got)
	}
}

func TestExportSummary(t *testing.T) {
	dir := t.TempDir()
	e := NewExporter(dir)
	want := engine.RunSummary{
		SchemaVersion:   1,
		Language:        "cpp",
		Status:          "partial",
		Files:           engine.FileCounts{Candidate: 2, Succeeded: 1, Failed: 1},
		ElementsByKind:  map[string]int{"CLASS": 1},
		RelationsByType: map[string]int{"CALL": 2},
		Stages:          []engine.StageTiming{{Name: "scan", DurationMS: 3}},
	}
	if err := e.ExportSummary(want); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, FileSummary))
	if err != nil {
		t.Fatal(err)
	}
	var got engine.RunSummary
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.Status != "partial" || got.Files.Candidate != 2 || got.ElementsByKind["CLASS"] != 1 {
		t.Errorf("summary = %+v", got)
	}
	if !strings.Contains(string(data), "\n  \"language\": \"cpp\"") {
		t.Errorf("summary should be indented JSON:\n%s", data)
	}

	// 原子写不应残留临时文件。
	entries, _ := os.ReadDir(dir)
	for _, ent := range entries {
		if strings.HasSuffix(ent.Name(), ".tmp") {
			t.Errorf("leftover temp file: %s", ent.Name())
		}
	}
}
