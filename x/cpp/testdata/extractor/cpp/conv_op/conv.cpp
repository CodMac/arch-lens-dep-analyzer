/* 转换运算符：转换目标类型建模为返回类型 → RETURN 关系（含类外定义）。 */

namespace cv {

struct Target {
    int v;
};

class Holder {
public:
    operator Target() const;
    explicit operator bool() const;
};

Holder::operator Target() const { return Target{0}; }

} // namespace cv
