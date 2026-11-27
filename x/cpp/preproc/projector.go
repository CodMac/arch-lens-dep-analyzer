// Package preproc 实现 C/C++ 源码的「条件编译投影」：
// 在交给语法分析器之前，把每组 #if/#ifdef/#ifndef/#elif/#else 中未被选中的分支
// 按字节替换为空格（保持文件长度与行列号不变），使 tree-sitter 只看到一条
// 语法完整的分支，避免「预处理分支劈裂语法结构」导致的 root-level ERROR。
//
// 设计要点：
//   - 投影结果与输入**等长**，因此 Location（行列号）无需任何映射；
//   - 指令行本身也置空格，避免 # token 进入 AST；
//   - 未选中分支中的 #include/#define/#undef/#pragma 默认保留（可关闭），
//     以保证 IMPORT 关系不因投影而丢失；
//   - #if 0 组整组丢弃（含全部 #elif/#else 与其中的 include）。
package preproc

import (
	"bytes"
	"regexp"
)

// Pass 指定分支选择策略。两遍投影（First + Last）可互补符号集。
type Pass int

const (
	// PassFirst 每组选第一个括号平衡的分支（默认）。
	PassFirst Pass = iota
	// PassLast 每组选最后一个括号平衡的分支（用于分支回补）。
	PassLast
)

// Options 控制投影行为。
type Options struct {
	// Pass 分支选择策略。
	Pass Pass
	// Branch 指定统一的「分支序号」（1 起）。>0 时优先于 Pass：
	// 每组尽量选第 Branch 个分支，组内分支不足时回退到 Pass 策略。
	// 用于分支回补：依次取 1,2,3,... 覆盖 #if/#elif/#else 全部分支。
	// 零值表示按 Pass 策略（保持 Options{} 字面量的既有语义）。
	Branch int
	// KeepIncludes 为 true 时保留未选中分支中的 #include/#define/#undef/#pragma 行。
	// 默认 true：保证 IMPORT 关系完整。
	KeepIncludes bool
}

// DefaultOptions 返回默认选项：首分支 + 保留 include。
func DefaultOptions() Options {
	return Options{Pass: PassFirst, KeepIncludes: true}
}

// Region 描述一个「被选中」的条件分支体区间（半开区间 [Start, End)）。
// 用于给元素回填 cpp.condition：元素起始字节落在哪个 Region 内，就属于哪个分支。
type Region struct {
	Start int
	End   int
	Label string // 例如 "#ifdef _WIN32" / "#else" / "#elif defined(X)"
	Depth int    // 嵌套深度，0 为最外层
	// Dead 标记 #if 0 死区：该区间内的代码永远不会被编译。
	// Collector/Extractor 据此跳过死区内的元素与关系。
	Dead bool
}

// MacroAnn 记录一次「类型注解宏归一化」。
type MacroAnn struct {
	Macro string // 被置空的宏名，如 "GTEST_API_"
	Type  string // 类型名，如 "Test"
	Pos   int    // 宏 token 的起始字节
}

// Result 是投影产物。
type Result struct {
	// Source 投影后的源码，长度与输入完全一致。
	Source []byte
	// Regions 被选中的分支体区间（按 Start 升序）。
	Regions []Region
	// Dropped 被丢弃的分支体区间（用于诊断/分支回补）。
	Dropped []Region
	// Dead #if 0 死区区间（整组丢弃，永不参与编译）。
	Dead []Region
	// MacroAnns 类型注解宏归一化记录。
	MacroAnns []MacroAnn
}

// directiveRe 匹配行首的条件编译指令。
var directiveRe = regexp.MustCompile(`^[ \t]*#[ \t]*(ifdef|ifndef|if|elif|else|endif)\b`)

// keepRe 匹配在未选中分支中仍保留的指令。
var keepRe = regexp.MustCompile(`^[ \t]*#[ \t]*(include|define|undef|pragma)\b`)

// ifZeroRe 匹配语义上恒假的 #if 条件。
var ifZeroRe = regexp.MustCompile(`^#[ 	]*if[ 	]+\(?[ 	]*0[ 	]*\)?[ 	]*(?://.*|/\*.*\*/)?[ 	]*$`)

type branch struct {
	start  int // 分支体起始字节（指令行之后）
	end    int // 分支体结束字节（下一个指令行起始）
	label  string
	chosen bool
}

type group struct {
	start    int // 组起始字节（#if 行）
	label    string
	branches []branch
	depth    int
	dead     bool // #if 0：整组丢弃
}

// Project 使用默认选项投影。
func Project(src []byte) Result {
	return ProjectWithOptions(src, DefaultOptions())
}

// ProjectWithOptions 按指定选项投影源码。
func ProjectWithOptions(src []byte, opts Options) Result {
	groups := scanGroups(src)
	chosen := make(map[int]int, len(groups))
	for _, g := range groups {
		chosen[g.start] = chooseBranch(g, src, opts)
	}

	out := make([]byte, len(src))
	copy(out, src)

	var regions, dropped, dead []Region
	for _, g := range groups {
		ci := chosen[g.start]
		for i, b := range g.branches {
			r := Region{Start: b.start, End: b.end, Label: b.label, Depth: g.depth}
			if g.dead {
				// #if 0：仅 #if 分支恒死；#elif/#else 分支无法静态判定，
				// 按「多收」原则保留（记为 dropped，非死区），由原始树补元素。
				if i == 0 {
					r.Dead = true
					dead = append(dead, r)
				} else {
					dropped = append(dropped, r)
				}
				continue
			}
			if i == ci {
				regions = append(regions, r)
			} else {
				dropped = append(dropped, r)
			}
		}
	}

	blankUnselected(out, src, groups, chosen, opts)
	sortRegions(regions)
	sortRegions(dropped)
	sortRegions(dead)

	out, anns := normalizeTypeMacros(out)

	return Result{Source: out, Regions: regions, Dropped: dropped, Dead: dead, MacroAnns: anns}
}

// MaxBranchCount 返回文件中单个条件组的最大分支数（无组时为 0）。
// 分支回补据此决定需要额外投影几遍。
func MaxBranchCount(src []byte) int {
	max := 0
	for _, g := range scanGroups(src) {
		if g.dead {
			continue
		}
		if n := len(g.branches); n > max {
			max = n
		}
	}
	return max
}

// scanGroups 扫描全部条件编译组（栈式配对）。
func scanGroups(src []byte) []*group {
	var groups []*group
	var stack []*group
	pos := 0
	inBlockComment := false
	inRawString := false
	rawDelim := ""
	continued := false

	for pos < len(src) {
		eol := bytes.IndexByte(src[pos:], '\n')
		if eol < 0 {
			eol = len(src)
		} else {
			eol += pos
		}
		line := src[pos:eol]

		isDirective := false
		// 原始字符串字面量内部的行首 `#` 不是预处理指令（B2）。
		if !continued && !inBlockComment && !inRawString {
			if m := directiveRe.FindSubmatch(line); m != nil {
				isDirective = true
				kw := string(m[1])
				label := string(bytes.TrimSpace(line))
				switch kw {
				case "ifdef", "ifndef", "if":
					g := &group{start: pos, label: label, depth: len(stack)}
					g.branches = append(g.branches, branch{start: eol + 1, end: eol + 1, label: label})
					groups = append(groups, g)
					stack = append(stack, g)
				case "elif", "else":
					if len(stack) > 0 {
						g := stack[len(stack)-1]
						g.branches[len(g.branches)-1].end = pos
						g.branches = append(g.branches, branch{start: eol + 1, end: eol + 1, label: label})
					}
				case "endif":
					if len(stack) > 0 {
						g := stack[len(stack)-1]
						g.branches[len(g.branches)-1].end = pos
						stack = stack[:len(stack)-1]
					}
				}
			}
		}

		// 更新块注释/原始字符串与续行状态（仅用于避免把注释/宏体/原始字符串内的 # 当指令）。
		if !isDirective {
			updateLexState(line, &inBlockComment, &inRawString, &rawDelim)
			continued = endsWithBackslash(line)
		} else {
			continued = false
		}

		pos = eol + 1
	}

	// 未闭合的组：最后一个分支延伸到文件末尾。
	for _, g := range stack {
		g.branches[len(g.branches)-1].end = len(src)
	}

	// 标记 #if 0 组。
	for _, g := range groups {
		if ifZeroRe.MatchString(g.label) {
			g.dead = true
		}
	}
	return groups
}

// chooseBranch 返回选中的分支下标；-1 表示整组丢弃。
func chooseBranch(g *group, src []byte, opts Options) int {
	if g.dead {
		return -1
	}
	// 指定分支序号：组内存在该分支且括号平衡时优先选它。
	if opts.Branch > 0 && opts.Branch <= len(g.branches) {
		i := opts.Branch - 1
		b := g.branches[i]
		if bracketBalance(src, b.start, b.end) == 0 {
			return i
		}
	}
	idx := 0
	found := false
	for i, b := range g.branches {
		if bracketBalance(src, b.start, b.end) == 0 {
			idx = i
			found = true
			if opts.Pass == PassFirst {
				break
			}
		}
	}
	if !found {
		return 0
	}
	return idx
}

// blankUnselected 把未选中分支的行与指令行按字节置空格。
func blankUnselected(out, src []byte, groups []*group, chosen map[int]int, opts Options) {
	// 指令行起始字节 → 所属组（用于快速判定当前行是否为指令行）。
	groupAt := make(map[int]*group, len(groups))
	for _, g := range groups {
		groupAt[g.start] = g
	}

	pos := 0
	var stack []*group
	inBlockComment := false
	inRawString := false
	rawDelim := ""
	for pos < len(src) {
		eol := bytes.IndexByte(src[pos:], '\n')
		if eol < 0 {
			eol = len(src)
		} else {
			eol += pos
		}
		line := src[pos:eol]

		if !inBlockComment && !inRawString {
			if m := directiveRe.FindSubmatch(line); m != nil {
				switch string(m[1]) {
				case "ifdef", "ifndef", "if":
					if g, ok := groupAt[pos]; ok {
						stack = append(stack, g)
					}
				case "endif":
					if len(stack) > 0 {
						stack = stack[:len(stack)-1]
					}
				}
				// 指令行整行置空；若为续行指令（如多行 #define/#if），其续行也必须
				// 一并置空，否则残留的宏体/条件片段会变成游离代码导致 syntax ERROR。
				blank(out, pos, eol)
				for endsWithBackslash(line) {
					pos = eol + 1
					if pos >= len(src) {
						break
					}
					eol = bytes.IndexByte(src[pos:], '\n')
					if eol < 0 {
						eol = len(src)
					} else {
						eol += pos
					}
					line = src[pos:eol]
					blank(out, pos, eol)
				}
				pos = eol + 1
				continue
			}
		}

		// 非指令行：更新块注释/原始字符串状态，供后续行判定指令。
		updateLexState(line, &inBlockComment, &inRawString, &rawDelim)

		active, inDead := true, false
		for _, g := range stack {
			if g.dead {
				// 只有 #if 分支是死区；#elif/#else 分支按未选中分支处理（非死区）。
				b0 := g.branches[0]
				if pos >= b0.start && pos < b0.end {
					active, inDead = false, true
				} else {
					active = false
				}
				break
			}
			ci := chosen[g.start]
			if ci < 0 {
				active, inDead = false, true
				break
			}
			b := g.branches[ci]
			if pos < b.start || pos >= b.end {
				active = false
				break
			}
		}

		if !active {
			keep := opts.KeepIncludes && !inDead && keepRe.Match(line)
			if !keep {
				blank(out, pos, eol)
			} else {
				// 保留的指令若是续行宏（行尾 `\`），其续行必须一并保留：
				// 否则悬挂的反斜杠会把后续行（乃至 #endif）吞进宏体，
				// 使条件组永不闭合，语法结构被破坏。
				for endsWithBackslash(line) {
					pos = eol + 1
					if pos >= len(src) {
						break
					}
					eol = bytes.IndexByte(src[pos:], '\n')
					if eol < 0 {
						eol = len(src)
					} else {
						eol += pos
					}
					line = src[pos:eol]
				}
			}
		}
		pos = eol + 1
	}
}

// blank 把 [from, to) 区间内的字节替换为空格（保留换行符）。
func blank(b []byte, from, to int) {
	if from < 0 {
		from = 0
	}
	if to > len(b) {
		to = len(b)
	}
	for i := from; i < to; i++ {
		if b[i] != '\n' {
			b[i] = ' '
		}
	}
}

// bracketBalance 返回区间内 ()[]{} 的净差值绝对值。
// 跳过行注释、块注释、字符串与字符字面量。
func bracketBalance(src []byte, from, to int) int {
	depth := 0
	i := from
	for i < to && i < len(src) {
		c := src[i]
		switch c {
		case '/':
			if i+1 < to && src[i+1] == '/' {
				for i < to && src[i] != '\n' {
					i++
				}
				continue
			}
			if i+1 < to && src[i+1] == '*' {
				i += 2
				for i+1 < to && !(src[i] == '*' && src[i+1] == '/') {
					i++
				}
				i += 2
				continue
			}
		case '"', '\'':
			quote := c
			i++
			for i < to && src[i] != quote {
				if src[i] == '\\' {
					i++
				}
				i++
			}
			i++
			continue
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
		}
		i++
	}
	if depth < 0 {
		return -depth
	}
	return depth
}

// updateLexState 跟踪块注释与原始字符串字面量状态。
// 原始字符串 R"delim( ... )delim" 可跨行，其内部的 `#if 0` 等行首文本不是指令（B2）。
func updateLexState(line []byte, inBlock *bool, inRaw *bool, rawDelim *string) {
	i := 0
	for i < len(line) {
		if *inRaw {
			end := []byte(")" + *rawDelim + "\"")
			if j := bytes.Index(line[i:], end); j >= 0 {
				i += j + len(end)
				*inRaw = false
				*rawDelim = ""
				continue
			}
			return
		}
		if *inBlock {
			j := bytes.Index(line[i:], []byte("*/"))
			if j < 0 {
				return
			}
			i += j + 2
			*inBlock = false
			continue
		}
		if i+1 < len(line) && line[i] == '/' && line[i+1] == '/' {
			return
		}
		if i+1 < len(line) && line[i] == '/' && line[i+1] == '*' {
			*inBlock = true
			i += 2
			continue
		}
		// 普通字符串/字符字面量：整体跳过，避免把其中的 R"( 误判为原始字符串起点。
		if line[i] == '"' || line[i] == '\'' {
			quote := line[i]
			i++
			for i < len(line) {
				if line[i] == '\\' {
					i += 2
					continue
				}
				if line[i] == quote {
					i++
					break
				}
				i++
			}
			continue
		}
		// 原始字符串起点：R"delim(（允许 u8/u/U/L 前缀，前缀不影响识别）。
		if line[i] == 'R' && i+1 < len(line) && line[i+1] == '"' {
			j := i + 2
			for j < len(line) && line[j] != '(' && line[j] != '"' && j-i-2 <= 16 {
				j++
			}
			if j < len(line) && line[j] == '(' {
				*inRaw = true
				*rawDelim = string(line[i+2 : j])
				i = j + 1
				continue
			}
		}
		i++
	}
}

// endsWithBackslash 判断行尾是否为宏续行。
func endsWithBackslash(line []byte) bool {
	t := bytes.TrimRight(line, " \t\r")
	return len(t) > 0 && t[len(t)-1] == '\\'
}

// sortRegions 按 Start 升序排序（插入排序，区间数量级为千，足够）。
func sortRegions(rs []Region) {
	for i := 1; i < len(rs); i++ {
		for j := i; j > 0 && rs[j].Start < rs[j-1].Start; j-- {
			rs[j], rs[j-1] = rs[j-1], rs[j]
		}
	}
}
