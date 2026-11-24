package engine

import (
	"sort"
	"time"
)

// FileOutcome 记录单个文件在本次分析中的最终状态。
// Err 为空表示成功；Stage 标识失败发生的阶段（scan / collector / extractor）。
type FileOutcome struct {
	Path       string `json:"path"`
	Stage      string `json:"stage,omitempty"`
	Error      string `json:"error,omitempty"`
	Elements   int    `json:"elements"`
	Relations  int    `json:"relations"`
	DurationMS int64  `json:"duration_ms"`
}

// StageTiming 记录单个流水线阶段的耗时。
type StageTiming struct {
	Name       string `json:"name"`
	DurationMS int64  `json:"duration_ms"`
}

// FileCounts 是文件级计数汇总。
type FileCounts struct {
	Candidate int `json:"candidate"`
	Succeeded int `json:"succeeded"`
	Failed    int `json:"failed"`
}

// RunSummary 是分析任务的结果概览，序列化为 analysis_summary.json。
type RunSummary struct {
	SchemaVersion   int            `json:"schema_version"`
	Language        string         `json:"language"`
	RootPath        string         `json:"root_path"`
	Filter          string         `json:"filter"`
	FilterLevel     int            `json:"filter_level"`
	Concurrency     int            `json:"concurrency"`
	StartedAt       string         `json:"started_at"`
	FinishedAt      string         `json:"finished_at"`
	DurationMS      int64          `json:"duration_ms"`
	Status          string         `json:"status"`
	Files           FileCounts     `json:"files"`
	Elements        int            `json:"elements"`
	Relations       int            `json:"relations"`
	ElementsByKind  map[string]int `json:"elements_by_kind"`
	RelationsByType map[string]int `json:"relations_by_type"`
	Stages          []StageTiming  `json:"stages"`
	Error           string         `json:"error,omitempty"`
}

// recordFileOutcome 写入（或覆盖）单个文件的结果。
func (ec *EngineContext) recordFileOutcome(o FileOutcome) {
	ec.outcomeMu.Lock()
	defer ec.outcomeMu.Unlock()
	if ec.fileOutcomes == nil {
		ec.fileOutcomes = make(map[string]*FileOutcome)
	}
	cp := o
	ec.fileOutcomes[o.Path] = &cp
}

// markFileSuccess 记录文件解析+收集成功。
func (ec *EngineContext) markFileSuccess(path string, elements int, d time.Duration) {
	ec.recordFileOutcome(FileOutcome{
		Path:       path,
		Elements:   elements,
		DurationMS: d.Milliseconds(),
	})
}

// markFileFailure 记录文件在指定阶段失败。
func (ec *EngineContext) markFileFailure(path, stage string, err error, d time.Duration) {
	ec.recordFileOutcome(FileOutcome{
		Path:       path,
		Stage:      stage,
		Error:      err.Error(),
		DurationMS: d.Milliseconds(),
	})
}

// markFileStageError 在已成功的文件上追加后续阶段错误（保留元素数）。
func (ec *EngineContext) markFileStageError(path, stage string, err error, d time.Duration) {
	ec.outcomeMu.Lock()
	defer ec.outcomeMu.Unlock()
	if o, ok := ec.fileOutcomes[path]; ok {
		o.Stage = stage
		o.Error = err.Error()
		o.DurationMS += d.Milliseconds()
		return
	}
	cp := FileOutcome{Path: path, Stage: stage, Error: err.Error(), DurationMS: d.Milliseconds()}
	ec.fileOutcomes[path] = &cp
}

// markFileRelations 回填文件产出的关系数。
func (ec *EngineContext) markFileRelations(path string, n int) {
	ec.outcomeMu.Lock()
	defer ec.outcomeMu.Unlock()
	if o, ok := ec.fileOutcomes[path]; ok {
		o.Relations = n
	}
}

// fileSucceeded 判断文件是否已成功收集（供 Extractor 跳过失败文件）。
func (ec *EngineContext) fileSucceeded(path string) bool {
	ec.outcomeMu.Lock()
	defer ec.outcomeMu.Unlock()
	o, ok := ec.fileOutcomes[path]
	return ok && o.Error == ""
}

// FileOutcomes 返回按路径排序的文件结果快照。
func (ec *EngineContext) FileOutcomes() []FileOutcome {
	ec.outcomeMu.Lock()
	defer ec.outcomeMu.Unlock()
	out := make([]FileOutcome, 0, len(ec.fileOutcomes))
	for _, o := range ec.fileOutcomes {
		out = append(out, *o)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// RecordStageDuration 记录阶段耗时（流水线串行执行，无需加锁）。
func (ec *EngineContext) RecordStageDuration(name string, d time.Duration) {
	ec.StageTimings = append(ec.StageTimings, StageTiming{
		Name:       name,
		DurationMS: d.Milliseconds(),
	})
}
