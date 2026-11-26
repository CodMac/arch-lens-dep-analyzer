/* Resolver 成员名不应回退为类型名样例。 */

struct field {
    int run() const;
};

void g() {
    // zz 类型未知：zz.field 是成员访问，不应把成员名 field 当类型。
    zz.field.run();
}
