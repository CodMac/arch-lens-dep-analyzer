// 构造器初始化列表被条件编译劈裂。
namespace demo {

class Conn {
 public:
  Conn(Conn &&other) noexcept
      : sock(other.sock)
#ifdef SSL_ENABLED
        , session(other.session)
#endif
  {
#ifdef SSL_ENABLED
    other.session = nullptr;
#endif
  }
 private:
  int sock;
};

}
