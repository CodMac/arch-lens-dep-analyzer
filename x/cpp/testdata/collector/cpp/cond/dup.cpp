// 多树重复：同一函数体在投影树与原始树中都存在，
// 动作关系必须按字节坐标去重，不能翻倍。
namespace dupzone {

void g() {
  int x = 0;
#ifdef A
  a(x);
#else
  b(x);
#endif
}

}
