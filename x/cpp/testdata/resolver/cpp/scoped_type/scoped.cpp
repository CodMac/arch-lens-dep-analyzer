/* 同文件内不同 namespace 的同名类型：使用处应按词法作用域解析。 */
namespace A {
struct Foo {
    int x;
};
} // namespace A

namespace B {
struct Foo {
    int y;
};

struct D : Foo {};

void f() {
    Foo *p = new Foo();
    (void)p;
    (void)(Foo *)0;
    throw Foo();
}
} // namespace B
