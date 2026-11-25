/* 同一函数在多个头文件重复声明，应全部与定义建 ImplLink。 */
#ifndef LINKER_IMPL_MA_H
#define LINKER_IMPL_MA_H
namespace md {
int shared(int x);
}
#endif
