/* 初始化表达式中的裸标识符是使用，不是声明点。 */
int iu_gv;

enum IuColor { IU_RED, IU_GREEN };

void iu_use() {
    int x = iu_gv;
    IuColor c = IU_RED;
    (void)x;
    (void)c;
}
