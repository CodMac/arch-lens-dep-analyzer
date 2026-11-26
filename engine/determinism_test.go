package engine_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/CodMac/arch-lens-dep-analyzer/core"
	"github.com/CodMac/arch-lens-dep-analyzer/engine"
	"github.com/CodMac/arch-lens-dep-analyzer/model"
	_ "github.com/CodMac/arch-lens-dep-analyzer/x/cpp"
)

// TestCollectorRegistrationDeterministic 验证并行 Collector 的注册顺序不影响解析结果：
// 两个文件分别在 A/B 命名空间定义同名 Dup，第三个文件以非限定名使用 Dup。
// 全局短名回退对同名符号取首个，若按完成顺序注册，目标会在 A::Dup / B::Dup 间抖动。
func TestCollectorRegistrationDeterministic(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"a.cpp": "namespace A { struct Dup {}; }\n",
		"b.cpp": "namespace B { struct Dup {}; }\n",
		"c.cpp": "Dup* p;\n",
	}
	var paths []string
	for name, src := range files {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(src), 0644); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}

	impl, err := core.Get(core.Language("cpp"))
	if err != nil {
		t.Fatal(err)
	}

	var first string
	for i := 0; i < 20; i++ {
		ec, err := engine.RunDefault(engine.Config{
			Language:    core.Language("cpp"),
			RootPath:    dir,
			FilePaths:   paths,
			Concurrency: 4,
			FilterLevel: core.LevelRaw,
		}, func() (core.Parser, error) {
			return impl.Parser.NewParser(), nil
		})
		if err != nil {
			t.Fatalf("run %d: %v", i, err)
		}

		got := ""
		for _, rel := range ec.Relations {
			// 只看 c.cpp 对 Dup 的类型引用（TYPE_ARG），排除 Linker 拓扑边的顺序干扰。
			if rel.Type == model.TypeArg && rel.Source != nil && rel.Source.Path == "c.cpp" &&
				rel.Target != nil && rel.Target.Name == "Dup" {
				got = rel.Target.QualifiedName
				break
			}
		}
		if got == "" {
			t.Fatalf("run %d: no relation targeting Dup", i)
		}
		if i == 0 {
			first = got
			continue
		}
		if got != first {
			t.Fatalf("run %d: Dup resolved to %q, first run %q", i, got, first)
		}
	}
}
