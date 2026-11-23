/* 签名流水线：模板实参剥离、后缀保留、空格归一、$N 位置。 */

template <typename Dtype>
struct Blob {
    Dtype v;
};

/* 参数含模板实参：QN 剥离实参但保留指针/引用 */
template <typename Dtype>
void tpl_param(const Blob<Dtype>& b, Blob<Dtype>* pb, Blob<Dtype>&& rb) {}

/* 嵌套模板实参 */
void nested_tpl(std::map<int, std::vector<float>> m) {}

/* 关键字间空格保留，* & 前空格去除 */
void spacing(const char * s, unsigned int n, const Blob<int> & b) {}

/* 模板实参 + 数组后缀 */
void tpl_array(Blob<int> arr[], Blob<int>* ptrs[]) {}

/* 同名同签名重复定义：$N 插在参数列表之前 */
int dup_sig(int a, int b) { return a + b; }
int dup_sig(int a, int b) { return a + b; }
