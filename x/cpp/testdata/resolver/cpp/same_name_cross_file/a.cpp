/* 跨文件同名类型：a.cpp 内的 SncfDup 必须解析到本文件定义。 */
struct SncfDup {
    int a;
};

void sncf_use_a() {
    SncfDup d;
    (void)d;
}
