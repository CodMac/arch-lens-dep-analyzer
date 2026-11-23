// 跨死区 ERROR 节点：链式调用中间夹 #if 0 时，tree-sitter 的错误恢复会产出
// 一个起点在死区内、终点在区外的巨型 ERROR 节点。死区判定必须要求「完全包含」，
// 否则会把 #endif 之后的正常代码整段误判为死区而丢弃。
namespace deadspan {

void before() { before_call(); }

void chain() {
  builder()
      .step1()
#if 0
      .dead_step()
#endif
      .step2()
      .step3();
}

void after() { after_call(); }

}
