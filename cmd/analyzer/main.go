// Package main 是 arch-lens-dep-analyer 的命令行入口（薄 CLI 层）。
// 只做参数解析、文件扫描、调用 engine 流水线并交给 output 导出，不承载任何分析逻辑。
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/CodMac/arch-lens-dep-analyer/core"
	"github.com/CodMac/arch-lens-dep-analyer/engine"
	"github.com/CodMac/arch-lens-dep-analyer/model"
	"github.com/CodMac/arch-lens-dep-analyer/output"
	_ "github.com/CodMac/arch-lens-dep-analyer/x/cpp"  // 注册 C/C++ 语言实现
	_ "github.com/CodMac/arch-lens-dep-analyer/x/java" // 注册 Java 语言实现
)

// Config 命令行配置。
type Config struct {
	Lang        string
	SourcePath  string
	Filter      string
	Jobs        int
	OutDir      string
	FilterLevel int
	Strict      bool
}

func main() {
	os.Exit(run())
}

// run 执行一次完整分析，返回进程退出码。
// 0：全部成功或部分成功（best-effort）；1：全局失败、全部文件失败或 -strict 下存在失败文件。
func run() int {
	cfg := parseFlags()
	totalStart := time.Now()

	// 1. 扫描文件
	stepStart := time.Now()
	fmt.Fprintf(os.Stderr, "[1/4] 🔍 正在扫描目录: %s\n", cfg.SourcePath)
	files, effectiveFilter, scanErrs, err := scanFiles(cfg.SourcePath, cfg.Filter, cfg.Lang)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ 扫描文件失败: %v\n", err)
		return 1
	}
	scanDur := time.Since(stepStart)
	fmt.Fprintf(os.Stderr, "    找到 %d 个候选文件 (耗时: %v)\n", len(files), scanDur.Round(time.Millisecond))

	absRoot, _ := filepath.Abs(cfg.SourcePath)
	relFiles := make([]string, 0, len(files))
	for _, f := range files {
		relFiles = append(relFiles, relPathOf(absRoot, f))
	}
	exporter := output.NewExporter(cfg.OutDir)
	if err := exporter.ExportFileList(output.FileScanFiles, relFiles); err != nil {
		fmt.Fprintf(os.Stderr, "❌ 写入扫描清单失败: %v\n", err)
		return 1
	}

	if len(files) == 0 {
		fmt.Fprintf(os.Stderr, "❌ 没有找到待分析的源文件: 路径 %s 下无匹配文件\n", cfg.SourcePath)
		_ = exporter.ExportFileOutcomes(nil, scanErrs)
		_ = writeReport(cfg, effectiveFilter, nil, len(files), 0, len(scanErrs), scanDur, totalStart, time.Now(), "failed", fmt.Errorf("没有找到待分析的源文件"))
		return 1
	}

	// 2. 组装语言实现并执行五阶段流水线
	stepStart = time.Now()
	fmt.Fprintf(os.Stderr, "[2/4] ⚙️  正在分析代码符号与关系 (Level: %d)...\n", cfg.FilterLevel)
	impl, err := core.Get(core.Language(cfg.Lang))
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ 获取语言实现失败: %v\n", err)
		_ = exporter.ExportFileOutcomes(nil, scanErrs)
		_ = writeReport(cfg, effectiveFilter, nil, len(files), 0, len(scanErrs), scanDur, totalStart, time.Now(), "failed", err)
		return 1
	}

	ec, err := engine.RunDefault(engine.Config{
		Language:    core.Language(cfg.Lang),
		RootPath:    cfg.SourcePath,
		FilePaths:   files,
		Concurrency: cfg.Jobs,
		FilterLevel: core.FilterLevel(cfg.FilterLevel),
		BestEffort:  !cfg.Strict,
	}, func() (core.Parser, error) {
		return impl.Parser.NewParser(), nil
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ 分析执行失败: %v\n", err)
		var succeeded, failed []engine.FileOutcome
		if ec != nil {
			succeeded, failed = splitOutcomes(ec.FileOutcomes())
		}
		failed = append(failed, scanErrs...)
		sort.Slice(failed, func(i, j int) bool { return failed[i].Path < failed[j].Path })
		_ = exporter.ExportFileOutcomes(succeeded, failed)
		_ = writeReport(cfg, effectiveFilter, ec, len(files), len(succeeded), len(failed), scanDur, totalStart, time.Now(), "failed", err)
		return 1
	}
	fmt.Fprintf(os.Stderr, "    分析完成 (耗时: %v)\n", time.Since(stepStart).Round(time.Millisecond))

	// 3. 执行导出逻辑
	stepStart = time.Now()
	fmt.Fprintf(os.Stderr, "[3/4] 💾 正在写入结果文件...\n")
	ecCount, relCount, err := runExport(cfg, ec)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ 导出失败: %v\n", err)
		_ = exporter.ExportFileOutcomes(nil, scanErrs)
		_ = writeReport(cfg, effectiveFilter, ec, len(files), 0, len(scanErrs), scanDur, totalStart, time.Now(), "failed", err)
		return 1
	}
	fmt.Fprintf(os.Stderr, "    ✅ 完成: 导出实体=%d, 最终关系=%d (耗时: %v)\n", ecCount, relCount, time.Since(stepStart).Round(time.Millisecond))

	// 4. 文件清单与任务概览
	succeeded, failed := splitOutcomes(ec.FileOutcomes())
	failed = append(failed, scanErrs...)
	sort.Slice(failed, func(i, j int) bool { return failed[i].Path < failed[j].Path })
	if err := exporter.ExportFileOutcomes(succeeded, failed); err != nil {
		fmt.Fprintf(os.Stderr, "❌ 写入文件清单失败: %v\n", err)
		return 1
	}
	status := "success"
	switch {
	case len(succeeded) == 0:
		status = "failed"
	case len(failed) > 0:
		status = "partial"
	}
	if err := writeReport(cfg, effectiveFilter, ec, len(files), len(succeeded), len(failed), scanDur, totalStart, time.Now(), status, nil); err != nil {
		fmt.Fprintf(os.Stderr, "❌ 写入任务概览失败: %v\n", err)
		return 1
	}
	if len(failed) > 0 {
		fmt.Fprintf(os.Stderr, "    ⚠️  %d 个文件失败，详见 %s\n", len(failed), output.FileScanFailed)
	}

	// 5. 总计
	fmt.Fprintf(os.Stderr, "\n[4/4] ✨ 所有任务执行完毕! 总耗时: %v\n", time.Since(totalStart).Round(time.Millisecond))

	if status == "failed" {
		return 1
	}
	if cfg.Strict && len(failed) > 0 {
		return 1
	}
	return 0
}

// parseFlags 解析命令行参数。
func parseFlags() Config {
	c := Config{}
	flag.StringVar(&c.Lang, "lang", "java", "分析语言")
	flag.StringVar(&c.SourcePath, "path", ".", "源码根路径")
	flag.StringVar(&c.Filter, "filter", "", "文件过滤正则")
	flag.IntVar(&c.Jobs, "jobs", runtime.NumCPU(), "并发数")
	flag.StringVar(&c.OutDir, "out-dir", "./output", "输出目录")
	flag.IntVar(&c.FilterLevel, "level", 1, "过滤等级: 0(Raw), 1(Balanced), 2(Pure)")
	flag.BoolVar(&c.Strict, "strict", false, "任一文件失败即返回非零退出码（默认 best-effort）")
	flag.Parse()
	return c
}

// runExport 将分析结果以 JSONL 导出到输出目录。
func runExport(cfg Config, ec *engine.EngineContext) (int, int, error) {
	if err := os.MkdirAll(cfg.OutDir, 0755); err != nil {
		return 0, 0, err
	}

	exporter := output.NewExporter(cfg.OutDir)
	rels := ec.Relations

	// 元素列表：GlobalContext 的全部定义。
	defs := ec.GC.Definitions()
	elements := make([]*model.CodeElement, 0, len(defs))
	for _, def := range defs {
		elements = append(elements, def.Element)
	}

	return exporter.ExportJsonL(elements, rels)
}

// splitOutcomes 按是否有错误拆分文件结果（保持输入顺序）。
func splitOutcomes(outcomes []engine.FileOutcome) (succeeded, failed []engine.FileOutcome) {
	for _, o := range outcomes {
		if o.Error == "" {
			succeeded = append(succeeded, o)
		} else {
			failed = append(failed, o)
		}
	}
	return succeeded, failed
}

// writeReport 组装并写出 analysis_summary.json。
func writeReport(cfg Config, filter string, ec *engine.EngineContext, candidate, succeeded, failed int,
	scanDur time.Duration, started, finished time.Time, status string, runErr error) error {
	s := engine.RunSummary{
		SchemaVersion:   1,
		Language:        cfg.Lang,
		RootPath:        cfg.SourcePath,
		Filter:          filter,
		FilterLevel:     cfg.FilterLevel,
		Concurrency:     cfg.Jobs,
		StartedAt:       started.Format(time.RFC3339),
		FinishedAt:      finished.Format(time.RFC3339),
		DurationMS:      finished.Sub(started).Milliseconds(),
		Status:          status,
		Files:           engine.FileCounts{Candidate: candidate, Succeeded: succeeded, Failed: failed},
		ElementsByKind:  map[string]int{},
		RelationsByType: map[string]int{},
	}
	if runErr != nil {
		s.Error = runErr.Error()
	}
	s.Stages = append(s.Stages, engine.StageTiming{Name: "scan", DurationMS: scanDur.Milliseconds()})
	if ec != nil {
		s.Stages = append(s.Stages, ec.StageTimings...)
		defs := ec.GC.Definitions()
		s.Elements = len(defs)
		for _, def := range defs {
			if def.Element != nil {
				s.ElementsByKind[string(def.Element.Kind)]++
			}
		}
		s.Relations = len(ec.Relations)
		for _, rel := range ec.Relations {
			if rel != nil {
				s.RelationsByType[string(rel.Type)]++
			}
		}
	}
	return output.NewExporter(cfg.OutDir).ExportSummary(s)
}

// scanFiles 按正则过滤扫描目录下的源文件。
// 未指定 filter 时，按语言的扩展名列表生成默认正则（多扩展名语言如 C/C++ 需一次匹配多个扩展名）。
// 返回实际使用的 filter 与遍历过程中的路径错误（不中断扫描）。
func scanFiles(rootPath, filter, lang string) ([]string, string, []engine.FileOutcome, error) {
	if filter == "" {
		exts, ok := langExtensions[lang]
		if !ok {
			// 未登记语言沿用历史行为：lang 值即扩展名。
			exts = []string{lang}
		}
		quoted := make([]string, 0, len(exts))
		for _, ext := range exts {
			quoted = append(quoted, regexp.QuoteMeta(strings.TrimPrefix(ext, ".")))
		}
		filter = fmt.Sprintf(`.*\.(%s)$`, strings.Join(quoted, "|"))
	}
	re, err := regexp.Compile(filter)
	if err != nil {
		return nil, filter, nil, err
	}

	absRoot, _ := filepath.Abs(rootPath)

	var files []string
	var scanErrs []engine.FileOutcome
	err = filepath.Walk(absRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			rel := relPathOf(absRoot, path)
			if path == absRoot {
				// 根目录本身不可访问时，相对路径退化为 "."，改用用户传入的根路径。
				rel = filepath.ToSlash(rootPath)
			}
			scanErrs = append(scanErrs, engine.FileOutcome{
				Path:  rel,
				Stage: "scan",
				Error: err.Error(),
			})
			return nil
		}
		if !info.IsDir() && re.MatchString(path) {
			files = append(files, path)
		}
		return nil
	})
	sort.Strings(files)
	return files, filter, scanErrs, err
}

// relPathOf 返回 path 相对项目根的 '/' 分隔路径。
func relPathOf(absRoot, path string) string {
	rel, err := filepath.Rel(absRoot, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
}

// langExtensions 各语言的默认源文件扩展名列表（无自定义 filter 时的扫描依据）。
var langExtensions = map[string][]string{
	"java": {".java"},
	"go":   {".go"},
	"c":    {".c", ".h"},
	"cpp":  {".cpp", ".hpp", ".cc", ".cxx", ".h"},
}
