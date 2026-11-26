package engine

import (
	"fmt"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/CodMac/arch-lens-dep-analyzer/core"
	"github.com/CodMac/arch-lens-dep-analyzer/model"
)

// =============================================================================
// 阶段 1: Collector（收集符号定义，文件级并行）
// =============================================================================

// CollectorStage 并行解析源文件并收集符号定义到全局上下文。
type CollectorStage struct {
	scheduler *Scheduler
}

// NewCollectorStage 创建 Collector 阶段。
func NewCollectorStage(scheduler *Scheduler) *CollectorStage {
	return &CollectorStage{scheduler: scheduler}
}

// Name 返回阶段名。
func (s *CollectorStage) Name() string { return "collector" }

// Concurrent 返回 true（文件级并行）。
func (s *CollectorStage) Concurrent() bool { return true }

// Execute 并行收集。
// 注意：解析/收集使用扫描得到的绝对路径，但 FileContext 内部统一以相对项目根的
// 路径登记（与 Extractor 的查找键、Linker/Exporter 的展示一致）。
//
// 注册顺序确定性：并行完成的文件上下文先暂存，全部完成后按相对路径排序再串行注册。
// 全局索引（如「定义优先」主索引）对同 QN 先到先得，若按完成顺序注册，跨文件同名
// 符号的解析结果会随调度抖动（同一份代码每次分析可能不同）。排序注册使结果可复现。
func (s *CollectorStage) Execute(ec *EngineContext) error {
	absRoot, _ := filepath.Abs(ec.Config.RootPath)

	var mu sync.Mutex
	var pending []collectedFile

	err := s.scheduler.Run(ec.Config.FilePaths, func(path string, p core.Parser) error {
		relPath := relPathOf(absRoot, path)
		start := time.Now()

		src, err := p.ParseFile(path)
		if err != nil {
			ec.markFileFailure(relPath, "collector", err, time.Since(start))
			if ec.Config.BestEffort {
				return nil
			}
			return err
		}

		fc, err := ec.Impl.Collector.CollectDefinitions(src)
		if err != nil {
			ec.markFileFailure(relPath, "collector", err, time.Since(start))
			if ec.Config.BestEffort {
				return nil
			}
			return err
		}
		normalizeFilePath(fc, relPath)

		mu.Lock()
		pending = append(pending, collectedFile{relPath: relPath, fc: fc})
		mu.Unlock()
		ec.markFileSuccess(relPath, len(fc.Definitions()), time.Since(start))
		return nil
	})

	// 按相对路径排序后串行注册，保证全局索引与定义顺序可复现。
	sort.Slice(pending, func(i, j int) bool { return pending[i].relPath < pending[j].relPath })
	for _, c := range pending {
		ec.GC.RegisterFileContext(c.fc)
	}
	return err
}

// collectedFile 是并行收集阶段暂存的文件上下文，待排序后统一注册。
type collectedFile struct {
	relPath string
	fc      core.FileContext
}

// relPathOf 返回 path 相对项目根的 '/' 分隔路径；无法相对化时退回原路径。
func relPathOf(absRoot, path string) string {
	rel, err := filepath.Rel(absRoot, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
}

// normalizeFilePath 将 FileContext 内全部路径统一为相对项目根的路径，并用 '/' 作分隔符：
//   - FileContext.FilePath 本身（全局注册表的键）
//   - 文件元素（Kind==File）的 QualifiedName（与 extractHierarchy 的查找键一致）
//   - 各元素定义的 Path 与 Location.FilePath
//   - 各 Import 记录的 Location.FilePath（collector 阶段记录的是扫描时的绝对路径）
//
// 语言 SPI（Collector）不感知项目根，相对化是引擎的编排职责。
func normalizeFilePath(fc core.FileContext, relPath string) {
	slashRel := filepath.ToSlash(relPath)
	fc.SetFilePath(slashRel)
	for _, def := range fc.Definitions() {
		el := def.Element
		if el == nil {
			continue
		}
		if el.Kind == model.File {
			el.QualifiedName = slashRel
		}
		el.Path = slashRel
		if el.Location != nil {
			el.Location.FilePath = slashRel
		}
	}
	for _, imps := range fc.Imports() {
		for _, imp := range imps {
			if imp.Location != nil {
				imp.Location.FilePath = slashRel
			}
		}
	}
}

// =============================================================================
// 阶段 2: Binder（符号绑定，串行）
// =============================================================================

// BinderStage 遍历全局定义完成类型绑定。
type BinderStage struct{}

// NewBinderStage 创建 Binder 阶段。
func NewBinderStage() *BinderStage { return &BinderStage{} }

// Name 返回阶段名。
func (s *BinderStage) Name() string { return "binder" }

// Concurrent 返回 false（串行）。
func (s *BinderStage) Concurrent() bool { return false }

// Execute 执行符号绑定。
func (s *BinderStage) Execute(ec *EngineContext) error {
	return ec.Impl.Binder.BindSymbols(ec.GC)
}

// =============================================================================
// 阶段 3: Extractor（提取关系，文件级并行）
// =============================================================================

// ExtractorStage 并行提取每个文件的依赖关系，并统一清理临时字段。
type ExtractorStage struct {
	scheduler *Scheduler
}

// NewExtractorStage 创建 Extractor 阶段。
func NewExtractorStage(scheduler *Scheduler) *ExtractorStage {
	return &ExtractorStage{scheduler: scheduler}
}

// Name 返回阶段名。
func (s *ExtractorStage) Name() string { return "extractor" }

// Concurrent 返回 true（文件级并行）。
func (s *ExtractorStage) Concurrent() bool { return true }

// Execute 并行提取关系。
func (s *ExtractorStage) Execute(ec *EngineContext) error {
	absRoot, _ := filepath.Abs(ec.Config.RootPath)
	var mu sync.Mutex

	err := s.scheduler.Run(ec.Config.FilePaths, func(path string, p core.Parser) error {
		relPath := relPathOf(absRoot, path)
		// 收集阶段失败的文件没有 FileContext，跳过以免二次报错。
		if !ec.fileSucceeded(relPath) {
			return nil
		}

		start := time.Now()
		rels, err := ec.Impl.Extractor.Extract(relPath, ec.GC)
		if err != nil {
			ec.markFileStageError(relPath, "extractor", err, time.Since(start))
			if ec.Config.BestEffort {
				return nil
			}
			return err
		}

		mu.Lock()
		ec.Relations = append(ec.Relations, rels...)
		mu.Unlock()
		ec.markFileRelations(relPath, len(rels))
		return nil
	})

	// 无论成功与否，统一清理临时字段，杜绝泄漏。
	ec.ClearTempFields()
	return err
}

// =============================================================================
// 阶段 4: Linker（层级拓扑，串行）
// =============================================================================

// LinkerStage 补齐层级关系。
type LinkerStage struct{}

// NewLinkerStage 创建 Linker 阶段。
func NewLinkerStage() *LinkerStage { return &LinkerStage{} }

// Name 返回阶段名。
func (s *LinkerStage) Name() string { return "linker" }

// Concurrent 返回 false（串行）。
func (s *LinkerStage) Concurrent() bool { return false }

// Execute 生成层级拓扑关系并追加到 Relations。
func (s *LinkerStage) Execute(ec *EngineContext) error {
	ec.Relations = append(ec.Relations, ec.Impl.Linker.LinkHierarchy(ec.GC)...)
	return nil
}

// =============================================================================
// 阶段 5: NoiseFilter（降噪，串行）
// =============================================================================

// NoiseFilterStage 按过滤等级移除噪音关系。
type NoiseFilterStage struct{}

// NewNoiseFilterStage 创建 NoiseFilter 阶段。
func NewNoiseFilterStage() *NoiseFilterStage { return &NoiseFilterStage{} }

// Name 返回阶段名。
func (s *NoiseFilterStage) Name() string { return "noise_filter" }

// Concurrent 返回 false（串行）。
func (s *NoiseFilterStage) Concurrent() bool { return false }

// Execute 执行降噪。
func (s *NoiseFilterStage) Execute(ec *EngineContext) error {
	filter := ec.Impl.NoiseFilter
	filter.SetLevel(ec.Config.FilterLevel)

	// LevelRaw 直通。
	if ec.Config.FilterLevel == core.LevelRaw {
		return nil
	}

	result := make([]*model.DependencyRelation, 0, len(ec.Relations))
	for _, rel := range ec.Relations {
		if !filter.IsNoise(*rel) {
			result = append(result, rel)
		}
	}
	ec.Relations = result
	return nil
}

// =============================================================================
// 便捷：构建标准五阶段流水线
// =============================================================================

// NewDefaultPipeline 构建标准五阶段流水线，复用同一调度器。
func NewDefaultPipeline(concurrency int, parserFn func() (core.Parser, error)) *Pipeline {
	sched := NewScheduler(concurrency, parserFn)
	p := NewPipeline()
	p.AddStage(NewCollectorStage(sched))
	p.AddStage(NewBinderStage())
	p.AddStage(NewExtractorStage(sched))
	p.AddStage(NewLinkerStage())
	p.AddStage(NewNoiseFilterStage())
	return p
}

// RunDefault 便捷入口：构建标准流水线并运行。
func RunDefault(cfg Config, parserFn func() (core.Parser, error)) (*EngineContext, error) {
	ec, err := NewContext(cfg)
	if err != nil {
		return nil, fmt.Errorf("create engine context: %w", err)
	}

	pipeline := NewDefaultPipeline(cfg.Concurrency, parserFn)
	if err := pipeline.Run(ec); err != nil {
		// 返回已填充的上下文，供调用方导出已完成部分与失败清单。
		return ec, err
	}
	return ec, nil
}
