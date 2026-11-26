/* Resolver 全局短名回退按实参个数挑选重载样例。 */

namespace ns {

int h(int x);
int h(int x, int y);

} // namespace ns

int caller() {
    // 非限定 h 在全局作用域找不到，回退全局短名时应按 2 个实参挑选。
    return h(1, 2);
}
