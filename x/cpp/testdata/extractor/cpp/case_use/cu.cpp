/* case 标签中的枚举常量是真实使用。 */

enum Color {
    RED,
    GREEN
};

void f(Color c) {
    switch (c) {
        case RED:
            break;
        case GREEN:
            break;
        default:
            break;
    }
}
