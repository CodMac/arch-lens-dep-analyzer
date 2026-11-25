/* 多词基本类型必须识别为 PRIMITIVE，而不是外部类型。 */
struct P {
    unsigned long long a;
    long double b;
    unsigned int c;
    signed char d;
    long long e;
    volatile int f;
};

void g(unsigned long long x, long double y) {}
