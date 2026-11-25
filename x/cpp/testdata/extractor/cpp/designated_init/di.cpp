/* 指定初始化 P p{.x = 1} 的字段应产 ASSIGN。 */

struct DiPoint {
    int x;
    int y;
};

void di_fn() {
    DiPoint p{.x = 1, .y = 2};
}
