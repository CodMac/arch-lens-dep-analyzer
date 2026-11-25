/* 类外/跨文件定义应作为文件根，产 File → 元素 边。 */
#ifndef LINKER_OUT_OF_LINE_H
#define LINKER_OUT_OF_LINE_H
namespace ol {
class Svc {
public:
    int m();
};
int freef(int x);
}
#endif
