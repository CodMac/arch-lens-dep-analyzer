// 包含守卫：选守卫体，#else 丢弃。
#ifndef DEMO_GUARD_H
#define DEMO_GUARD_H

class Guarded { public: Guarded(); };

#else

class Fallback { public: Fallback(); };

#endif
