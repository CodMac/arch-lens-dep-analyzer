/* ImplLink 签名规范化：typedef 别名 / struct tag / 数组↔指针 应配对，
   const/引用/指针重载应各自区分。 */
#ifndef LINKER_IMPL_SIG_H
#define LINKER_IMPL_SIG_H

struct SigPoint {
    int v;
};

typedef int SigInt;

int sig_alias(SigInt v);
int sig_tag(struct SigPoint p);
int sig_arr(int a[]);
int sig_ov(SigPoint s);
int sig_ov(const SigPoint& s);
int sig_ov(SigPoint* s);

#endif

int sig_unsigned(unsigned v);
int sig_long(long v);
