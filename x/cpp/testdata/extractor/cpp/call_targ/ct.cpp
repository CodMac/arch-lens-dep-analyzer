/* 调用表达式的模板实参应产 TYPE_ARG。 */

struct CtFoo {
    int v;
};

void ct_fn() {
    auto p = make_shared<CtFoo>();
}
