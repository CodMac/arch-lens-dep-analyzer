/* using 声明与命名空间别名应产 USE 依赖。 */

namespace udns {
void ud_free();
}

struct UdBase {
    void ud_method();
};

struct UdDerived : UdBase {
    using UdBase::ud_method;
};

namespace ud_alias = udns;

void ud_fn() {
    using udns::ud_free;
}
