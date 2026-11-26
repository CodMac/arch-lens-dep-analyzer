/* Resolver 返回类型链式调用样例：外部类型与源码内类型。 */
#include <string>

namespace ret {

std::string makeName() { return std::string(); }

/* 外部返回类型：makeName().size() → std::string::size */
int useExternalChain() {
    return (int)makeName().size();
}

struct Svc {
    int run() const;
};

Svc makeSvc();

/* 源码内返回类型：makeSvc().run() → ret::Svc::run() */
int useSourceChain() {
    return makeSvc().run();
}

} // namespace ret
