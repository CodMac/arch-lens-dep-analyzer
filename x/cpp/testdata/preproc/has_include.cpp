// __has_include 无法静态判定：选第一个括号平衡分支。
// 未选中分支中的 #include 默认保留（保证 IMPORT 关系完整）。
#ifdef _WIN32
#  if __has_include(<afunix.h>)
#    include <afunix.h>
#    define HAVE_AFUNIX 1
#  endif
#else
#  include <sys/un.h>
#endif

namespace demo {
class Socket { public: Socket(); };
}
