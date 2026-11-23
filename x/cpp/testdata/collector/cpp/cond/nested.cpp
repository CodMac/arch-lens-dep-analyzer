// 嵌套条件编译：4 个组合分支，每组各定义一个函数并调用一个外部函数。
// 投影树只能覆盖 (OUTER=1, INNER=1) 与 (OUTER=0, INNER=0) 两个组合，
// 原始树（保留 preproc_* 节点）可覆盖全部 4 个组合。
namespace nested {

#ifdef OUTER
  #ifdef INNER
    void f_1_1() { call_a(); }
  #else
    void f_1_2() { call_b(); }
  #endif
#else
  #ifdef INNER
    void f_2_1() { call_c(); }
  #else
    void f_2_2() { call_d(); }
  #endif
#endif

}
