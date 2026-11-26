/* Resolver 边界形态样例：函数指针 / 成员函数指针 / delete / 虚派发 / 智能指针 / auto。 */
#include <memory>

struct Svc {
    virtual int run();
    int stop();
};

struct Base {
    int value;
};

/* 函数指针调用：fp(1) 不建模为普通函数 CALL。 */
void callFuncPtr(int (*fp)(int)) {
    fp(1);
}

/* 成员函数指针调用：(s->*pm)(1) 不应产畸形外部 CALL。 */
void callMemberPtr(Svc *s, int (Svc::*pm)(void)) {
    (s->*pm)();
}

/* delete 不产关系。 */
void destroy(Svc *p) {
    delete p;
}

/* 虚派发按静态类型解析。 */
int dispatch(Svc *s) {
    return s->run();
}

/* 智能指针：p->run() 按包装类型解析（未解包）。 */
int smartPtr() {
    std::unique_ptr<Svc> p;
    return p->run();
}

Svc makeSvc();

/* auto 推导：s.run() 依赖文件短名/全局查找。 */
int autoVar() {
    auto s = makeSvc();
    return s.run();
}
