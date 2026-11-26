/* 跨文件同名类型：b.cpp 内的 SncfDup 必须解析到本文件定义。 */
struct SncfDup {
    int b;
};

void sncf_use_b() {
    SncfDup d;
    (void)d;
}
