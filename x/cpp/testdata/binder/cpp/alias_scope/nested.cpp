/* 嵌套类同名别名：内层遮蔽外层。 */
struct Outer {
    using alias_t = int;
    struct Inner {
        using alias_t = float;
        alias_t v;
    };
};

/* 模板别名底层退化为模板变量：保留别名 QN。 */
template <typename T>
using Vec = T*;

struct Holder {
    Vec<int> v;
};
