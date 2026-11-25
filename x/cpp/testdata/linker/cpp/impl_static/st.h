/* static 成员函数应参与 ImplLink；static 自由函数（内部链接）不参与。 */
#ifndef LINKER_IMPL_ST_H
#define LINKER_IMPL_ST_H
namespace sd {
class Svc {
public:
    static int make(int x);
};
static int local(int x);
}
#endif
