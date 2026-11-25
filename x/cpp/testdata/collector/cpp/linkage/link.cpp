/* 链接规范与 inline namespace。 */

inline namespace v1 {
struct InlineType {
    int x;
};
void inline_fn();
}

extern "C" {
void c_func(int);
int c_var;
struct CStruct {
    int v;
};
}

extern "C" void c_direct();
