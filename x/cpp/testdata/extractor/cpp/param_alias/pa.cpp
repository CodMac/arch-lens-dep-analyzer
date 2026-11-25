/* 命名参数的别名应按 Binder 的作用域化 QN 解析（内层遮蔽外层）。 */

struct XX {
    int a;
};

struct YY {
    int b;
};

struct O {
    using AA = XX;
    struct I {
        using AA = YY;
        void f(AA x);
    };
};
