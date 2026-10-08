# silo

> 像使用 Linux 终端一样操作 S3 / MinIO —— 一个用 Go 编写的交互式对象存储客户端。

`silo` 把对象存储映射成一棵虚拟文件系统树，你可以用 `ls`、`cd`、`cp`、`mv`、`rm`、
`cat` 等熟悉的命令来浏览和操作 bucket 与对象。它直接读写
[MinIO mc](https://github.com/minio/mc) 的配置文件（`~/.mc/config.json`），
**已有的 alias 无需重新配置即可复用**。

底层基于 [minio-go](https://github.com/minio/minio-go) SDK，纯 Go 实现，
**不依赖 `mc` 可执行文件**，可自由交叉编译到 Windows / Linux / macOS。

**English** → [README.en.md](README.en.md)

> 🤖 **本项目完全由 AI 开发，人类负责决策。** 需求、架构取舍与发布由人拍板，代码、测试与文档由 AI 编写。

## 一键安装

```bash
# Linux / macOS（无需 Go）
curl -fsSL https://raw.githubusercontent.com/nonex-code/silo/main/install.sh | sh

# Windows PowerShell
irm https://raw.githubusercontent.com/nonex-code/silo/main/install.ps1 | iex

# 已有 Go（任意平台）
go install github.com/nonex-code/silo@latest
```

---

## 目录

- [特性](#特性)
- [安装](#安装)
- [快速开始](#快速开始)
- [命令参考](#命令参考)
- [虚拟文件系统](#虚拟文件系统)
- [传输与断点续传](#传输与断点续传)
- [配置兼容](#配置兼容)
- [命令行参数](#命令行参数)
- [交互与快捷键](#交互与快捷键)
- [常见问题](#常见问题)
- [开发与测试](#开发与测试)
- [目录结构](#目录结构)
- [License](#license)

## 特性

- 🗂️ **虚拟文件系统** —— `/` 列出所有 alias，`/<alias>` 列出 bucket，
  `/<alias>/<bucket>/<key>` 直达对象；完整支持 `cd` / `pwd` / `..` / `~` / `cd -`。
- 🔀 **跨别名操作** —— `cp` / `mv` 可在不同 alias（甚至不同服务商）之间搬运数据：
  同一 alias 内走服务端 `CopyObject`（不落地），跨 alias 走下载 + 上传临时文件中转。
- 🚀 **可靠的传输**
  - 多文件并发，每个文件独立进度条；
  - 超过阈值的文件自动分片上传（Multipart Upload）；
  - 断点续传：上传状态持久化到 `<文件>.silo-upload.json`，下载使用 HTTP Range；
  - 可调并发数与分片大小。
- ⌨️ **顺手的交互**
  - Tab 补全：命令、alias、bucket、公共前缀、对象；
  - 命令历史（`~/.silo_history`，重启后保留）；
  - 彩色输出，可用 `--no-color` 关闭。
- 🧩 **mc 配置兼容** —— 复用同一份 `config.json`，保留未知字段，权限 `0600`，原子写入。
- 🖥️ **跨平台** —— Windows / Linux / macOS，无 cgo。
- 🤖 **可脚本化** —— `-c "命令"` 或从 stdin 传入多行命令，适合 CI。

## 安装

一键安装见上。其它方式：

```bash
go install github.com/nonex-code/silo@latest   # 需要 Go 1.27+
```

从源码构建：

```bash
git clone https://github.com/nonex-code/silo
cd silo
go build -o silo .        # Windows 下得到 silo.exe
make release              # 交叉编译全部平台到 dist/
```

## 快速开始

```bash
$ silo
silo 0.1.0  (type 'help' for commands, 'exit' to quit)
config: C:\Users\you\.mc\config.json

/ > ls /                        # 列出所有 alias
/ > use rufs                    # 进入某个 alias（等价于 cd /rufs）
/rufs > ls                      # 列出该 alias 下的 bucket
/rufs > cd my-bucket/logs
/rufs/my-bucket/logs > ls -l
/rufs/my-bucket/logs > cat app.log
/rufs/my-bucket/logs > get app.log ./app.log          # 下载
/rufs/my-bucket/logs > put ./new.log .                # 上传
/rufs/my-bucket/logs > cp app.log ooo/backup-bucket/  # 跨 alias 复制
/rufs/my-bucket/logs > mv old.log old-2025.log        # 同 alias 内重命名
/rufs/my-bucket/logs > rm -r tmp/                     # 递归删除前缀
```

新增 alias（写入 `~/.mc/config.json`，与 `mc` 完全一致）：

```bash
/ > alias set play https://play.min.io ACCESS_KEY SECRET_KEY
/ > alias set minio http://127.0.0.1:9000 minioadmin minioadmin --path on
/ > alias list
/ > alias remove play
```

## 命令参考

传输标志通用说明：`-r/--recursive` 递归、`-c/--continue` 断点续传、
`-n/--no-clobber` 目标存在则跳过、`--parallel N` 并发数（默认 4）。

### 导航

| 命令 | 说明 |
| --- | --- |
| `ls [-l] [-R] [-a] [path...]` | 列出 alias / bucket / 前缀 / 对象（别名 `dir`） |
| `ll [-R] [-a] [path...]` | 长格式列表：每行一个条目，含类型、大小、修改时间、ETag、存储类 |
| `cd [path]` | 切换目录；无参数回到 `/`，`cd -` 回到上一个目录 |
| `pwd` | 显示当前虚拟目录 |
| `tree [-L depth] [path]` | 以树形递归显示 |
| `use <alias>` | 进入某个 alias，等价于 `cd /<alias>` |

> `ls`/`ll` 默认隐藏以 `.` 开头的对象，加 `-a` 显示。`cd` 允许进入尚不存在的前缀
> （bucket 仍会校验），方便先 `cd newprefix` 再 `mkdir`/`put`。

### 对象

| 命令 | 说明 |
| --- | --- |
| `cat <file...>` | 输出对象内容 |
| `head [-n N] <file...>` | 输出前 N 行 |
| `tail [-n N] <file...>` | 输出后 N 行 |
| `wc [-l] [-w] [-c] <file...>` | 统计行数 / 词数 / 字节数 |
| `stat <path>` | 显示对象或前缀的详细信息（大小、ETag、存储类、版本、Owner 等） |
| `du [-a] [path...]` | 递归统计占用大小（`-a` 逐个列出对象） |
| `find [path] [-name P] [-type f\|d] [-size +N\|-N] [-maxdepth N]` | 递归查找 |
| `touch <file...>` | 创建空对象（或刷新已存在对象） |
| `mkdir [-p] <prefix...>` | 创建前缀（`-p` 补全父前缀）；在 alias 根下创建 bucket |
| `rmdir [-p] <prefix...>` | 删除**空**前缀（`-p` 一并清理空父前缀） |
| `rm [-r] [-f] [-n] <path...>` | 删除对象 / 前缀 / bucket（`-n` 试运行；别名 `delete`/`del`/`remove`） |
| `which <path>` | 解析路径并显示它是 object / directory / bucket / alias |
| `grep [-i] [-v] [-n] [-l] [-c] [-E] [--include G] [--exclude G] <pattern> <path...>` | 流式搜索对象内容，前缀会自动递归 |
| `hash [--type md5\|sha1\|sha256] <file...>` | 计算校验和（快捷命令 `md5` / `sha1` / `sha256`） |
| `presign [-e 7d] <file>` | 生成预签名下载链接 |

### 传输

| 命令 | 说明 |
| --- | --- |
| `put [-r] [-c] [-n] [--parallel N] <local...> [remote]` | 上传文件或目录（别名 `upload`） |
| `get [-r] [-c] [-n] [--parallel N] <remote...> [local]` | 下载对象或前缀（别名 `download`） |
| `cp [-r] [--parallel N] <source...> <target>` | 复制，支持跨 alias（别名 `copy`） |
| `mv [-r] [--parallel N] <source...> <target>` | 移动（复制后删除源，别名 `move`/`rename`） |
| `mirror [--overwrite] [--remove] [-n] [--include G] [--exclude G] [--parallel N] <source> <target>` | 增量同步本地目录与远端前缀（`-n` 试运行） |
| `diff [--include G] [--exclude G] <source> <target>` | 比较两个树（本地或远端）并列出差异 |

> `mirror` 的本地/远端由路径首段判断：以已配置的 alias 名开头（或 `/alias/...`）即视为远端，
> 否则视为本地路径。默认只补齐缺失或大小不同的对象，`--overwrite` 强制覆盖，
> `--remove` 删除目标端多出的对象。

### Bucket 与配置

| 命令 | 说明 |
| --- | --- |
| `mb <bucket...>` | 创建一个或多个 bucket |
| `rb [-f] <bucket...>` | 删除 bucket（`-f` 表示先清空） |
| `buckets [-a] [alias]` | 列出 bucket；`-a` 列出所有 alias 的 bucket |
| `alias [list \| set ... \| remove ...]` | 管理 mc alias（别名 `aliases`） |
| `config` | 显示当前使用的配置文件路径 |

### 其他

| 命令 | 说明 |
| --- | --- |
| `help [command]` | 命令列表或单条用法（末尾附别名对照表） |
| `history [-c]` | 查看（`-c` 清空）本次会话的命令历史 |
| `info` | 显示版本、配置文件、当前目录、分片大小、并发数 |
| `clear` | 清屏 |
| `echo <text...>` | 回显文本（调试用） |
| `version` | 显示版本 |
| `exit` | 退出（别名 `quit` / `q`，或按 `Ctrl-D`） |

### 别名

为了兼容 mc / Unix 习惯，silo 为常用命令提供了别名（`help` 末尾也会列出）：

| 别名 | 等价于 |
| --- | --- |
| `dir` / `list` | `ls` |
| `copy` | `cp` |
| `move` / `rename` | `mv` |
| `upload` | `put` |
| `download` | `get` |
| `remove` / `delete` / `del` | `rm` |
| `aliases` | `alias` |
| `quit` / `q` | `exit` |

## 虚拟文件系统

路径由 `alias / bucket / key` 三部分组成：

```
/                              → 所有 alias
/rufs                          → rufs 下的所有 bucket
/rufs/my-bucket                → my-bucket 下的顶级前缀与对象
/rufs/my-bucket/logs/app.log   → 一个具体对象
```

- 不以 `/` 开头的相对路径相对于当前目录解析，支持 `.`、`..`、`~`。
- `ls` 会把 S3 的「公共前缀」显示成以 `/` 结尾的目录，因此可以像本地目录一样 `cd`。
- S3 没有真正的目录：`mkdir` 通过写入零字节的 `prefix/` 标记对象实现。
- 跨 alias 的 `cp`/`mv` 会先把源对象下载到临时文件，再上传到目标 alias，
  因此大文件依然享受分片与断点续传；同一 alias 内则直接调用服务端复制。

## 传输与断点续传

- **单次 vs 分片**：对象大小 ≤ 32 MiB 时使用单次 PUT；更大时自动分片上传。
- **上传断点续传**：分片上传过程中会把 `UploadID`、分片大小、源文件大小/时间戳写入
  `<本地文件>.silo-upload.json`。再次执行 `put -c` 时，程序会查询服务端已成功接收的
  分片并跳过它们，完成后删除状态文件。
- **下载断点续传**：`get -c` 会先 `StatObject` 获取远端大小，与本地文件大小比较，
  然后用 HTTP `Range` 从断点继续追加。
- `--part-size 64M`、`--parallel 8` 可调整分片大小与并发度。
- 为兼容不支持 aws-chunked 流式签名的 S3 实现，分片上传与零字节对象使用显式
  `Content-Length` / SHA256 校验。

## 配置兼容

`silo` 与 `mc` 使用同一份配置文件。查找顺序如下，**若 `MC_CONFIG_DIR` 未设置则自动探测已有文件**：

1. `$MC_CONFIG_DIR/config.json`（若设置了该环境变量）
2. `~/.mc/config.json`（官方 mc 默认位置）
3. `~/mc/config.json`（部分 mc 发行版/衍生客户端，如某些 RustFS 工具）
4. `%APPDATA%\mc\config.json`、`%LOCALAPPDATA%\mc\config.json`（Windows）

若以上都不存在，则回退到 `~/.mc/config.json`（便于新建 alias）。也可以用
`--config <路径>` 显式指定；`config` 命令会显示当前实际使用的文件。

支持的 alias 字段：`url`、`accessKey`、`secretKey`、`api`（`S3v4` / `S3v2`）、
`path` / `lookup`（`auto` / `on` / `off` / `dns`，对应 path-style / virtual-host-style）、`region`。

写回时会做以下几点，保证 `silo` 与 `mc` 可以安全地交替使用：

- **保留未知字段**：配置文件顶层的未知键，以及 silo 未修改过的 alias 内的未知键，都会原样保留。
- **原子写入**：先写临时文件再替换，权限 `0600`（与 mc 一致）。
- **容错读取**：兼容 `version` 为字符串（`"10"`）或数字（`10`），并容忍 UTF-8 BOM。
- **环境别名**：支持 mc 的 `MC_HOST_<name>` 环境变量（见下），这类别名可读可用，
  但**不会写回文件**；`alias list` 的 `SOURCE` 列会显示 `env`。

### MC_HOST_* 环境别名

与 mc 一致，可以用环境变量临时定义 alias，无需写入配置文件：

```bash
# 格式：MC_HOST_<name>=scheme://ACCESS_KEY:SECRET_KEY@HOST[:PORT]
export MC_HOST_play='https://ACCESS_KEY:SECRET_KEY@play.min.io'
silo -c "ls /"        # 会额外列出 play
```

环境别名优先级高于文件中的同名 alias；若用 `alias set` 显式重设同名 alias，则会转为持久化到文件。

## 命令行参数

```
silo [flags] [command ...]

  --config string     指定 mc config.json 路径（默认自动查找）
  --alias string      启动时进入指定 alias
  -c string           执行一条命令后退出（也可把命令作为结尾参数传入）
  --no-color          关闭彩色输出
  --json              每条命令输出一个 JSON 对象（机器可读）
  --parallel int      默认传输并发数（默认 4）
  --part-size string  分片大小，支持 K/M/G 后缀，如 16M、64M（默认 16M）
  --history string    历史文件路径（默认 ~/.silo_history，也可用 SILO_HISTORY 环境变量）
  --version           显示版本并退出
  -h, --help          显示帮助
```

脚本 / CI / Agent 用法：

```bash
silo -c "ls /"
silo --config ./config.json --alias minio -c "find t/bucket -name '*.log'"
printf 'use minio\nls\n' | silo        # 从 stdin 读取多行命令后退出

# 机器可读：`--json` 对同一套命令输出单行 JSON，便于 Agent 解析
silo --json -c "ls /minio/bucket"
# {"command":"ls /minio/bucket","ok":true,"stdout":"a.txt\nb.txt\n"}
printf 'ls /\necho hi\n' | silo --json   # 每行命令一条 JSON（NDJSON）
```

**只有一套命令语法、一条执行路径**：交互式 REPL（`silo`）与一次性执行（`silo -c`）
都走同一个 `Eval`；`--json` / `--no-color` / 进度条只是输出层的开关，不影响命令行为。
Agent 侧规范写法就是 `silo -c "<一条和交互式完全相同的命令>"`。

退出码：成功为 `0`；命令失败（包括多目标时的部分失败）为 `1`；`grep` 无匹配为 `1`
（与 Unix `grep` 一致）。`--json` 的 `exit_code` 字段与进程退出码相同，`stderr` 非空
即表示有错误输出；命令本身返回的错误会写入 `error` 字段。

## 交互与快捷键

- **Tab**：补全命令名、alias、bucket、前缀与对象；以及各命令的旗标（输入 `-` 后）。
  `put` / `mirror` / `diff` 会同时补全本地路径与远端路径。
- **上/下箭头**：浏览历史命令。
- **Ctrl-C**：取消当前输入行，不会退出程序。
- **Ctrl-D**：退出。
- 历史记录保存在 `~/.silo_history`；`history -c` 会同时清空内存与文件。

## 常见问题

**Q：读不到我 mc 里已有的 alias？**
先用 `config` 命令查看 silo 实际使用的配置文件路径。silo 会自动探测 `~/.mc`、`~/mc`
以及 Windows 的 `%APPDATA%\mc`、`%LOCALAPPDATA%\mc`；若你的 mc 用了其它目录，设置
`MC_CONFIG_DIR` 环境变量或用 `--config <路径>` 指定即可。

**Q：需要安装 `mc` 吗？**
不需要。`silo` 只是读取 `mc` 的配置文件格式，所有网络操作都由 Go SDK 完成。

**Q：支持 AWS S3 之外的 S3 兼容服务吗？**
支持。MinIO、Ceph、阿里云 OSS、腾讯云 COS、Cloudflare R2 等只要兼容 S3 API 即可，
通过 `alias set` 的 `--path on` 可切换到 path-style 访问。

**Q：为什么上传目录时要加 `-r`？**
与 `cp`/`rm` 一致，递归操作需要显式开启，避免误传整个目录。

**Q：断点续传的上传状态文件可以删吗？**
可以。删除后再次上传会从头开始；上传成功后状态文件会自动删除。

**Q：能像 shell 一样用管道和重定向吗？**
不提供 `|`、`>` 等语法。需要组合时请使用 `-c` 与外部 shell，例如
`silo -c "cat t/bucket/a.log" > a.log`。

## 开发与测试

```bash
go build ./...
go vet ./...
gofmt -l .
go test ./...
```

集成测试使用 [gofakes3](https://github.com/johannesboyne/gofakes3) 在进程内启动一个
S3 兼容服务，覆盖虚拟文件系统、上传/下载、跨别名复制移动、分片上传、断点续传、
目录去重与 Tab 补全等场景，无需真实服务器。

推送版本标签会触发 GitHub Actions 交叉编译并自动创建 Release（附带各平台产物与校验和）：

```bash
git tag v0.2.0 && git push origin v0.2.0
```

## 目录结构

```
main.go                      CLI 入口：参数解析、信号处理、启动 REPL
install.sh / install.ps1     一键安装脚本（下载最新 Release）
.github/workflows/release.yml 标签触发编译、打包、发布
internal/config/             mc config.json 读写（兼容、保留未知字段）
internal/s3fs/               S3 客户端封装、列举、复制、分片与续传传输
internal/shell/              VFS 路径解析、命令实现、补全、历史、REPL
internal/ui/                 彩色输出与进度条
```

## License

[MIT](LICENSE)