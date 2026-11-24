package test

import (
	"testing"

	"github.com/CodMac/arch-lens-dep-analyzer/core"
	"github.com/CodMac/arch-lens-dep-analyzer/model"
	"github.com/CodMac/arch-lens-dep-analyzer/x/cpp/constants"
)

// =============================================================================
// 多树动作提取（原始树 + 投影树 + 字节坐标去重）
// 夹具：testdata/collector/cpp/cond/
// =============================================================================

// collectCond 收集 cond 场景并跑完整提取流水线。
func collectCond(t *testing.T) (core.GlobalContext, []*model.DependencyRelation) {
	t.Helper()
	paths := ResolvePaths(t, "collector/cpp/cond", "*.cpp")
	return RunExtraction(t, "x/cpp/testdata", paths)
}

// TestConditionalNestedActions 嵌套 4 组合分支内的动作关系不丢。
func TestConditionalNestedActions(t *testing.T) {
	gc, rels := collectCond(t)

	// 4 个组合各定义一个函数。
	for _, fn := range []string{
		"nested::f_1_1()", "nested::f_1_2()", "nested::f_2_1()", "nested::f_2_2()",
	} {
		AssertDef(t, gc, fn, model.Function)
	}

	// 每个函数各产 1 条 CALL 到对应外部函数。
	cases := map[string]string{
		"nested::f_1_1()": "call_a",
		"nested::f_1_2()": "call_b",
		"nested::f_2_1()": "call_c",
		"nested::f_2_2()": "call_d",
	}
	for src, target := range cases {
		FindRel(t, rels, func(r *model.DependencyRelation) bool {
			return r.Type == model.Call &&
				r.Source.QualifiedName == src &&
				r.Target.QualifiedName == target
		})
	}
}

// TestConditionalNestedConditions 4 个函数的 cpp.condition 标签正确。
func TestConditionalNestedConditions(t *testing.T) {
	gc := collectCpp(t, "cond")

	cases := map[string]string{
		"nested::f_1_1()": "#ifdef INNER",
		"nested::f_1_2()": "#else",
		"nested::f_2_1()": "#ifdef INNER",
		"nested::f_2_2()": "#else",
	}
	for qn, want := range cases {
		AssertMores(t, gc, qn, constants.Condition, want)
	}
}

// TestConditionalDeadZone #if 0 死区内的元素与关系都不入图。
func TestConditionalDeadZone(t *testing.T) {
	gc, rels := collectCond(t)

	AssertDef(t, gc, "deadzone::live()", model.Function)
	FindRel(t, rels, func(r *model.DependencyRelation) bool {
		return r.Type == model.Call &&
			r.Source.QualifiedName == "deadzone::live()" &&
			r.Target.QualifiedName == "live_call"
	})

	if DefExists(gc, "deadzone::dead_fn()") {
		t.Errorf("dead_fn() in #if 0 zone should not be collected")
	}
	if HasRel(rels, func(r *model.DependencyRelation) bool {
		return r.Target != nil && r.Target.QualifiedName == "dead_call"
	}) {
		t.Errorf("dead_call relation in #if 0 zone should not be emitted")
	}
}

// TestConditionalDeadZoneSpan 死区边界不得吞掉区外代码。
//
// tree-sitter 在 `#if 0` 处会产出起点在死区内、终点在区外的跨区 ERROR 节点；
// 若死区判定只看起始字节，`#endif` 之后的正常代码会被整段丢弃。
func TestConditionalDeadZoneSpan(t *testing.T) {
	gc, rels := collectCond(t)

	// 死区前后的函数都必须存在。
	AssertDef(t, gc, "deadspan::before()", model.Function)
	AssertDef(t, gc, "deadspan::after()", model.Function)
	AssertDef(t, gc, "deadspan::chain()", model.Function)

	// 死区后的调用关系不能丢。
	FindRel(t, rels, func(r *model.DependencyRelation) bool {
		return r.Type == model.Call &&
			r.Source.QualifiedName == "deadspan::after()" &&
			r.Target.QualifiedName == "after_call"
	})

	// 死区内的调用不产关系。
	if HasRel(rels, func(r *model.DependencyRelation) bool {
		return r.Target != nil && r.Target.QualifiedName == "dead_step"
	}) {
		t.Errorf("dead_step relation in #if 0 zone should not be emitted")
	}
}

// TestConditionalNoDuplicateActions 多树遍历不得使动作关系翻倍。
func TestConditionalNoDuplicateActions(t *testing.T) {
	_, rels := collectCond(t)

	// g() 内只有 a(x) 与 b(x) 两个调用（各在 #if/#else 一个分支）。
	var calls []*model.DependencyRelation
	for _, r := range rels {
		if r.Type != model.Call || r.Source == nil {
			continue
		}
		if r.Source.QualifiedName != "dupzone::g()" {
			continue
		}
		calls = append(calls, r)
	}
	if len(calls) != 2 {
		t.Errorf("dupzone::g() CALL count = %d, want 2 (a and b, deduped)", len(calls))
		for _, r := range calls {
			t.Logf("  call -> %s @ %s", r.Target.QualifiedName, r.Location.FilePath)
		}
	}

	// 全局：同一 (Type, Source, Target, Location) 不应出现两次。
	seen := make(map[string]int)
	for _, r := range rels {
		if r.Source == nil || r.Target == nil || r.Location == nil {
			continue
		}
		key := string(r.Type) + "|" + r.Source.QualifiedName + "|" +
			r.Target.QualifiedName + "|" + r.Location.FilePath + ":" +
			itoa(r.Location.StartLine)
		seen[key]++
	}
	for k, n := range seen {
		if n > 1 {
			t.Errorf("duplicate relation %s x%d", k, n)
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
