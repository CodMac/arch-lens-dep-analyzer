/* typedef/using 的底层类型是类型依赖。 */

struct TdBase {
    int v;
};

struct TdFoo {
    int f;
};

template <typename T>
struct TdBox {
    T t;
};

using TdAlias = TdBase;
typedef TdBox<TdFoo> TdBoxFoo;
typedef struct {
    int x;
} TdAnon;
