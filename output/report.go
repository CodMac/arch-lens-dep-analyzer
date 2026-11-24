package output

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"

	"github.com/CodMac/arch-lens-dep-analyzer/engine"
)

// 报告文件名（与 element.jsonl / relation_*.jsonl 同目录）。
const (
	FileScanFiles     = "scan_files.jsonl"
	FileScanSucceeded = "scan_succeeded.jsonl"
	FileScanFailed    = "scan_failed.jsonl"
	FileSummary       = "analysis_summary.json"
)

// filePathRecord 是文件清单的单行记录。
type filePathRecord struct {
	Path string `json:"path"`
}

// ExportFileList 将文件路径列表写为 JSONL（每行 {"path": "..."}）。
func (e *Exporter) ExportFileList(name string, paths []string) error {
	return e.writeJSONL(name, func(w *JSONLWriter) error {
		for _, p := range paths {
			if err := w.Write(filePathRecord{Path: p}); err != nil {
				return err
			}
		}
		return nil
	})
}

// ExportFileOutcomes 分别写出成功与失败文件清单。
func (e *Exporter) ExportFileOutcomes(succeeded, failed []engine.FileOutcome) error {
	if err := e.writeJSONL(FileScanSucceeded, func(w *JSONLWriter) error {
		for _, o := range succeeded {
			if err := w.Write(o); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}
	return e.writeJSONL(FileScanFailed, func(w *JSONLWriter) error {
		for _, o := range failed {
			if err := w.Write(o); err != nil {
				return err
			}
		}
		return nil
	})
}

// ExportSummary 写出分析任务概览 JSON。
func (e *Exporter) ExportSummary(s engine.RunSummary) error {
	return e.writeAtomic(FileSummary, func(w io.Writer) error {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(s)
	})
}

// writeJSONL 以 JSONL 形式原子写入 OutDir/name。
func (e *Exporter) writeJSONL(name string, fn func(w *JSONLWriter) error) error {
	return e.writeAtomic(name, func(w io.Writer) error {
		return fn(NewJSONLWriter(w))
	})
}

// writeAtomic 先写临时文件再重命名，避免留下半截文件。
func (e *Exporter) writeAtomic(name string, fn func(w io.Writer) error) error {
	if err := os.MkdirAll(e.OutDir, 0755); err != nil {
		return err
	}
	finalPath := filepath.Join(e.OutDir, name)
	tmpPath := finalPath + ".tmp"

	f, err := os.Create(tmpPath)
	if err != nil {
		return err
	}
	if err := fn(f); err != nil {
		f.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, finalPath); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return nil
}
