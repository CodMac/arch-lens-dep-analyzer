/* 单大写字母真实类型：typedef S value_type 的底层是 struct S，不是模板变量。 */
struct S {
    int x;
};

typedef S value_type;

struct A {
    value_type v;
};
