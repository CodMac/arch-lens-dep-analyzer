// 嵌套条件：内层在外层选中范围内独立决策。
namespace demo {

#ifdef OUTER
#  ifdef INNER
int nested_var;
#  endif
int outer_only;
#endif

#ifdef OTHER
int other_var;
#endif

}
