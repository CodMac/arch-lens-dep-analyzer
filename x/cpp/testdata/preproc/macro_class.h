// 类型注解宏：tree-sitter 会把带注解的类声明解析成函数。
namespace testing {

class GTEST_API_ Test {
 public:
  Test();
  int run(int x);
};

struct GTEST_API_ ConstCharPtr {
  const char* p;
};

}
