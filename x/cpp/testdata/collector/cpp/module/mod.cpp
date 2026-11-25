/* C++20 模块：module 声明 / import 头文件单元与具名模块 / 全局与私有片段。 */

export module demo.core;

import <vector>;
import "local.h";
import other.mod;
export import reexport.mod;

module;

module :private;

int answer() { return 42; }
