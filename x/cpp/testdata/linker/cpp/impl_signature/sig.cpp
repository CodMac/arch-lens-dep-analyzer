#include "sig.h"

int sig_alias(int v) { return v; }
int sig_tag(SigPoint p) { return p.v; }
int sig_arr(int* a) { return a ? *a : 0; }
int sig_ov(SigPoint s) { return s.v; }
int sig_ov(const SigPoint& s) { return s.v; }
int sig_ov(SigPoint* s) { return s->v; }

int sig_unsigned(unsigned int v) { return (int)v; }
int sig_long(long int v) { return (int)v; }
