/* 内联类型定义作为字段/变量类型：具名取标签 QN，匿名无 QN。 */
struct ItOuter {
    struct ItInner { int deep; } inner;
    union { int a; float b; } payload;
};

/* 别名与标签同名：typedef struct ItTagSame { ... } ItTagSame; */
typedef struct ItTagSame { int n; } ItTagSame;

void it_f(void) {
    struct { int m; } local_anon;
    enum { IT_E1 } local_e;
    (void)local_anon;
    (void)local_e;
}
