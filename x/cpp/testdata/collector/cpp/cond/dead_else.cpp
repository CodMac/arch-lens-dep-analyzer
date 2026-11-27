// #if 0 的 #else 分支是活跃代码，必须收集（多收原则）。
namespace deadele {

#if 0
void dead_branch() { dead_branch_call(); }
#else
void live_branch() { live_branch_call(); }
#endif

}
