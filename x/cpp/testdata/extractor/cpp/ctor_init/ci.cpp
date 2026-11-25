/* 栈对象构造 T x(args) / T x{args} 应产构造器 CALL。 */

struct Ctor {
    int v;
    Ctor(int);
    Ctor(int, int);
};

void ci_fn() {
    Ctor a(1);
    Ctor b{1, 2};
    Ctor c = Ctor(3);
}
