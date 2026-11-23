// #if 0 死区：死区内的函数与调用都不应产元素/关系。
namespace deadzone {

void live() { live_call(); }

#if 0
void dead_fn() { dead_call(); }
#endif

}
