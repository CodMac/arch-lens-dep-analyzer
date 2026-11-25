/* concept 的 underlying 是约束表达式，不能当作类型别名参与展开。 */

namespace cpt {
template <typename T>
concept Small = sizeof(T) <= 4;
}

struct Small {
    int v;
};

void use() {
    Small s;
}
