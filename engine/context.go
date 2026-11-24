// Package engine 实现五阶段流水线编排与并发调度。
package engine

import (
	"fmt"
	"sync"

	"github.com/CodMac/arch-lens-dep-analyzer/core"
	"github.com/CodMac/arch-lens-dep-analyzer/model"
)

// Config 描述一次完整分析运行的配置。
type Config struct {
	// Language 目标语言。
	Language core.Language

	// RootPath 项目根路径（用于计算相对路径）。
	RootPath string

	// FilePaths 待分析的源文件路径列表。
	FilePaths []string

	// Concurrency 文件级并行度（<=0 时使用默认 4）。
	Concurrency int

	// FilterLevel 噪音过滤等级。
	FilterLevel core.FilterLevel

	// BestEffort 为 true 时，单文件解析/收集/提取失败只记录不中止，
	// 其余文件继续分析；为 false 时保持「首个错误即终止」。
	BestEffort bool
}

// EngineContext 承载流水线各阶段之间的共享数据。
type EngineContext struct {
	Config Config

	// Impl 语言实现（从注册表获取，缓存避免重复查找）。
	Impl *core.LanguageImpl

	// GC 全局符号上下文。
	GC core.GlobalContext

	// Relations 已提取的依赖关系集合（阶段 3+ 填充）。
	Relations []*model.DependencyRelation

	// StageTimings 各流水线阶段的耗时（按执行顺序）。
	StageTimings []StageTiming

	// fileOutcomes 记录每个文件的最终状态（并发写，需加锁）。
	outcomeMu    sync.Mutex
	fileOutcomes map[string]*FileOutcome
}

// NewContext 根据配置创建引擎上下文。
func NewContext(cfg Config) (*EngineContext, error) {
	impl, err := core.Get(cfg.Language)
	if err != nil {
		return nil, err
	}
	if impl.NewGlobalContext == nil {
		return nil, fmt.Errorf("language %q does not provide NewGlobalContext", cfg.Language)
	}
	return &EngineContext{
		Config:       cfg,
		Impl:         impl,
		GC:           impl.NewGlobalContext(impl.SymbolResolver, impl.SymbolBuilder),
		fileOutcomes: make(map[string]*FileOutcome),
	}, nil
}

// ClearTempFields 清空全部关系的私有临时字段（流水线结束、导出前调用）。
func (ec *EngineContext) ClearTempFields() {
	for _, rel := range ec.Relations {
		if rel != nil {
			rel.ClearTemp()
		}
	}
}
