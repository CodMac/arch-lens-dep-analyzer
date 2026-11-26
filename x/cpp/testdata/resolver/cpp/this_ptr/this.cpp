/* this-> 成员访问：按宿主类型（含继承链）解析，类外定义同样适用。
   X 与 Base 含同名成员，用于验证不会退化为文件内首个同名成员。 */
struct X {
    int baseFld;
    void baseM() {}
};

struct Base {
    int baseFld;
    void baseM() {}
};

struct Derived : Base {
    void f();
};

void Derived::f() {
    this->baseM();
    this->baseFld = 2;
}
