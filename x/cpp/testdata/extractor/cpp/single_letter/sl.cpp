/* 单大写字母的真实类型名（T/B/E…）不应被当作模板类型变量。 */

struct T {
    int v;
};

struct U : T {
    int w;
};

T make_t();

void take_t(T t);

namespace n {
struct B {
    int x;
};
struct D : B {
    int y;
};
void take_b(B b);
}
