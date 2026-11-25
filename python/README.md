# python/ · Python 小工具放这里

## 这个文件夹是干什么的

放所有用 **Python** 写的小程序、命令行工具、数据处理脚本。想做一个"批量重命名图片""自动整理表格"这类工具，就放到这里，一个软件一个子文件夹。

## 里面每个文件是什么

| 文件/文件夹 | 是干什么的 |
| --- | --- |
| `hello.py` | 最小示例——运行它会打印 "Hello, world!"，可以照着它改 |
| `requirements.txt` | 依赖清单——记录这个工具需要额外安装哪些 Python 库 |
| `tests/` | 测试代码——检查功能有没有写错，`test_hello.py` 是示例 |

## 怎么运行

```bash
python python/hello.py
cd python && python -m unittest discover -s tests -v   # 跑测试
```

## 生成的文件在哪里

命令行工具一般直接输出结果，不生成单独的文件。如果工具会导出文件（比如处理后的表格），按命名铁律用中文名，并在软件的 README 里写清保存位置。
