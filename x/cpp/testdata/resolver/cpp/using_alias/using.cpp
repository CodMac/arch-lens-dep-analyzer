/* Resolver using 声明 / 命名空间别名样例。 */

namespace ns1 {

int f(int x);
int v;
struct T {};

} // namespace ns1

namespace ns2 {

int f(int x);
int v;
struct T {};

} // namespace ns2

namespace u1 {

using ns2::f;
using ns2::v;

int call() { return f(1); }
void setv() { v = 2; }

} // namespace u1

namespace u2 {

using ns2::T;

void mk() { T t; (void)t; }

} // namespace u2

namespace na = ns2;

int useAlias() { return na::f(1); }
