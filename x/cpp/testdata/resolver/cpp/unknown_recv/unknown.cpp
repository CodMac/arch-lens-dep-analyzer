/* 接收者类型未知：不得忽略接收者回退到非限定名查找。 */
struct Base {
    int baseField;
};

int freeFn(int a) { return a; }

void g() {
    zz.freeFn(1);   // zz 未声明：不应命中全局 freeFn
    zz.baseField = 1; // 不应命中 Base::baseField
}
