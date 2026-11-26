package context

import (
	"strings"

	"github.com/CodMac/arch-lens-dep-analyzer/core"
	"github.com/CodMac/arch-lens-dep-analyzer/model"
	"github.com/CodMac/arch-lens-dep-analyzer/x/java/constants"
)

// GlobalContext 是 Java 的全局符号上下文。
type GlobalContext struct {
	*core.BaseGlobalContext
}

// NewGlobalContext 创建 Java 全局上下文。
func NewGlobalContext(resolver core.SymbolResolver, builder core.SymbolBuilder) core.GlobalContext {
	return &GlobalContext{
		BaseGlobalContext: core.NewBaseGlobalContext(resolver, builder, NewDefinitionIndex(builder)),
	}
}

// RegisterFileContext 先注册包层级，再登记文件内定义（同一把写锁内完成）。
func (gc *GlobalContext) RegisterFileContext(fc core.FileContext) {
	gc.RegisterFileContextWith(fc, func() {
		if pfc, ok := fc.(PackageAwareFileContext); ok {
			if name := pfc.PackageName(); name != "" {
				gc.registerPackage(name)
			} else {
				gc.registerDefaultPackage()
			}
		}
	})
}

// registerPackage 将点号包名拆分为逐级 PACKAGE 元素（Java 专属语义）。
func (gc *GlobalContext) registerPackage(packageName string) {
	parts := strings.Split(packageName, ".")
	var current []string
	for _, part := range parts {
		if part == "" {
			continue
		}
		current = append(current, part)
		pkgQN := strings.Join(current, ".")
		gc.addPackageElem(part, pkgQN)
	}
}

// registerDefaultPackage 为默认包（无 package 声明）注册哨兵 PACKAGE 元素。
func (gc *GlobalContext) registerDefaultPackage() {
	gc.addPackageElem(constants.DefaultPackageQN, constants.DefaultPackageQN)
}

// addPackageElem 注册一个 PACKAGE 元素（幂等）。
// 调用方已持写锁，故使用 *Locked 变体。
func (gc *GlobalContext) addPackageElem(name, pkgQN string) {
	if _, ok := gc.FindByQualifiedNameLocked(pkgQN); ok {
		return
	}
	gc.AddDefinitionLocked(&core.DefinitionEntry{
		Element: &model.CodeElement{
			Kind:          model.Package,
			Name:          name,
			QualifiedName: pkgQN,
			IsFormSource:  true,
		},
	})
}
