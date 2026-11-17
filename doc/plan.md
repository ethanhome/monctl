# monctl 改造实施计划

设计见 [设计文档.md](设计文档.md)。每一步单独提交,完成后按“验收”逐项审核,通过后再进入下一步。

状态标记:`[ ]` 未开始,`[~]` 进行中,`[x]` 已完成并审核。

---

## 步骤 1:删除热键与开机启动 `[~]`(已在分支 `refactor/remove-hotkey-autostart` 实现,待审核)

**目的**:CLI 只保留 `scan`、`get`、`set`、`vcp`。

**前置**:动手前先列出具体要删除的文件和函数清单,经确认后再执行。

**任务**
- 删除 `watch.go`、`apply_worker*.go`、`hotkey_*.go`、`rawinput_windows.go`、`autostart*.go`、`axprompt_*.go`、`modsrelease_*.go`、`probe_input_*.go`、`config.go`。
- 删除 `monitor/monitor.go` 中仅服务热键的 `Config` 相关函数(先确认无其他引用)。
- `main.go` 去掉 `watch`、`config`、`autostart`、`probe-input` 子命令及 usage 文本。
- 清理 `main_darwin.go`、`main_other.go` 中的 `mainthread` 逻辑。
- `go.mod` 去掉 `golang.design/x/hotkey`、`golang.design/x/mainthread`,执行 `go mod tidy`。
- 删除 `tools/inject-mute.ps1`、`tools/osd/`、`tools/app/mkapp.sh`、`tools/app/Info.plist`。保留 `tools/app/mkdmg.sh`(步骤 7 复用)。
- 更新 README,删除热键、autostart、watch 相关内容。
- `ci.yml` 去掉 X11、Xvfb 步骤。
- `release.yml` 暂时只做最小改动,使其在删除代码后仍能构建通过:去掉 `-H windowsgui` 的 tray 产物,以及依赖 `mkapp.sh` 的 `.app` 打包步骤(此时先发布裸 CLI)。完整收窄放到步骤 7。

**验收**
- [ ] `go build ./...` 在 macOS 通过(Windows、Linux 交叉编译也通过)。
- [ ] `go vet ./...`、`go test ./...` 通过。
- [ ] `monctl --help` 只列出 `scan`、`get`、`set`、`vcp`。
- [ ] 全仓库搜索 `hotkey`、`autostart`、`mainthread`、`watch` 无残留引用(README 历史说明除外)。
- [ ] 真机:`scan`、`get`、`set` 行为与改造前一致。

---

## 步骤 2:抽出 `internal/service`,CLI 改为调用它 `[~]`(已实现,待审核)

**目的**:CLI 与 GUI 共用同一套业务逻辑。

**任务**
- 新建 `internal/service`,提供 `List`、`Get`、`Set`、`Raw`。
- 把 `main.go` 中 `cmdGetSet` 等函数里的值解析(百分比、`+N`/`-N` 偏移、名称、`0xNN`)和 VCP 最大值换算移入 service。
- 把 `main.go` 搬到 `cmd/monctl/`,只保留参数解析和输出格式化。
- 同步修改 `release.yml` 里的构建路径(`.` 改为 `./cmd/monctl`),避免 tag 发布时找不到入口。
- 迁移现有测试,并为解析逻辑补充单元测试。

**验收**
- [ ] CLI 外部行为(参数、输出、退出码)与步骤 1 结束时一致。
- [ ] service 不依赖 `fmt` 输出、`os.Exit` 或 `flag`。
- [ ] 解析逻辑有单元测试,覆盖百分比、偏移、名称、十六进制、越界和非法输入。
- [ ] `go build ./...`、`go test ./...` 通过。

---

## 步骤 3:新增 `internal/settings` `[~]`(已实现,待审核)

**目的**:保存输入源标签和“不再提示”选项。

**任务**
- 设置文件放在系统用户配置目录(`os.UserConfigDir()`),格式见设计文档第 5 节。
- 提供读取、保存标签、保存 `skipSwitchConfirm`。
- 显示器标识用 `monitor.Info.Description`(已确认 `Info` 没有序列号)。
- 文件不存在、损坏时返回默认值,不报错中断。
- 写入用临时文件加重命名,避免写一半损坏。

**验收**
- [ ] 单元测试覆盖:文件不存在、JSON 损坏、读写往返、多显示器、并发写。
- [ ] 不读写任何旧的热键配置文件。
- [ ] 设置文件里不含“上一个输入源”之类的状态字段。

---

## 步骤 4:初始化 Wails 项目,做显示器列表和输入源行 `[~]`(已实现,待审核)

**目的**:打通 Wails 与 `service`,先看到真实数据。

**任务**
- 在 `gui/` 初始化 Wails 项目,前端用原生 JS(不引入框架)。
- `app.go` 暴露 `ListMonitors()`、`GetSettings()`、`SaveLabel()`。
- 后端加串行 DDC 队列。
- 前端实现:显示器标题、刷新按钮、输入源垂直列表(当前口置顶并标记“当前”、有标签的口展示、其余折叠进“更多”)、常驻小字提示。
- 实现“扫描不到显示器”状态和“重新检测”。
- 标签点击编辑并保存。
- 未知输入值显示为原始值。

**验收**
- [ ] macOS 上 `wails dev` 和 `wails build` 都能运行(重点验证 CGO 与 `monitor/darwin.go` 共存)。
- [ ] 列表显示的当前输入源与 `monctl get input` 一致。
- [ ] 折叠规则正确:当前口、有标签的口始终显示,其余在“更多”内,重启后默认折叠。
- [ ] 标签保存后重启仍在。
- [ ] 无显示器或 DDC 不通时显示“未检测到可控制的显示器”。
- [ ] 窗口缩放时布局不出现横向滚动。

---

## 步骤 5:切换输入源 `[~]`(已实现,待审核)

**任务**
- 暴露 `SetInput(idx, name)`、`SetSkipSwitchConfirm(bool)`。
- 前端实现切换确认框,带“不再提示”复选框;勾选后不再弹。
- 确认后发送切换命令,**界面保持不变**(当前标记不动,亮度等控件仍显示)。
- 底部轻提示“已发送切换到 X”,几秒后自动消失。
- 切走之后再操作失败时,轻提示“无法与显示器通信,可能已切换到其他输入源”。

**验收**
- [ ] 点击当前口无反应(不可点击)。
- [ ] 确认框文案含目标输入源名称和标签。
- [ ] 勾选“不再提示”后重启仍生效。
- [ ] 切换后界面没有任何状态变化,没有“已切换”页面。
- [ ] 真机双机测试:A 切到 B,A 的界面不变;在 B 上切回 A,A 的界面仍与切走前一致。
- [ ] 不存在自动重试、自动切回的逻辑。

---

## 步骤 6:亮度、对比度、电源 `[~]`(已实现,待审核)

**任务**
- 暴露 `SetBrightness`、`SetContrast`、`SetPower`。
- 前端放在输入源下方,仅当读出了当前输入源时显示。
- 亮度、对比度中某一项读不出,只隐藏该项。
- 滑块前端防抖;后端串行队列中只保留最新一个值。
- 设置失败时滑块回到操作前的值,并提示“无法与显示器通信……”。
- 实现“能扫描到显示器但读不出当前输入源”的状态:输入源列表显示但无“当前”标记,亮度等整块隐藏,并给出提示。

**验收**
- [ ] 拖动滑块后,`monctl get brightness`、`get contrast` 读到的值一致。
- [ ] 快速连续拖动不会造成 DDC 并发或卡死。
- [ ] 电源开、待机、关三个选项行为正确(关闭前考虑是否需要确认,待审核时讨论)。
- [ ] 在已切走的状态下操作,失败后滑块回退并有提示,界面其他部分不变。
- [ ] 读不出当前输入源时,亮度、对比度、电源整块隐藏。

---

## 步骤 7:CI 与 Release 打包 `[~]`(已实现,待审核)

**目的**:用户只需要在 GitHub Release 下载对应平台的二进制安装。只支持 Windows 10/11(amd64)和 macOS arm64。详见设计文档 7.1。

**任务**
- `ci.yml`:
  - 矩阵保留 ubuntu、macos、windows;ubuntu 只跑单元测试。
  - 加入 GUI 构建检查:Windows、macOS 上执行 `wails build`。
  - 保留测试失败时输出 annotation 的步骤。
- `release.yml`(tag `v*` 触发):
  - 构建目标收窄为 `windows/amd64` 和 `darwin/arm64`,删除 `windows/arm64`、`darwin/amd64`、`linux/amd64`。
  - Windows:产出 `monctl-windows-amd64.exe`(CLI)和 `monctl-gui-windows-amd64.exe`(GUI)。GUI 需在 Windows runner 上构建。
  - macOS:产出 `monctl-darwin-arm64`(CLI 裸二进制,不再被 `.app` 取代)和 `monctl-macos-arm64.dmg`(GUI,Wails 生成 `.app`,用 `tools/app/mkdmg.sh` 打包)。
  - 注入版本号 `main.version`;确认代码里存在该变量,没有则补上。
  - 上传 `dist/*` 到 Release,自动生成 release notes。
- 确认 Windows GUI 对 WebView2 的处理方案(Wails 安装引导自动下载,或在 README 提示)。
- 更新 README:
  - 安装方式:各平台下载哪个文件。
  - CLI 用法、GUI 使用说明。
  - 多台电脑切换输入源的注意事项(切走后本机无法控制,需在目标电脑或显示器上切回)。
  - macOS 未签名时的放行方法(右键打开,或 `xattr -d com.apple.quarantine`)。

**验收**
- [ ] CI 在 Windows、macOS、Linux 上全绿(Linux 只跑单元测试)。
- [ ] 打测试 tag(如 `v0.0.0-test`)后,Release 里恰好有 4 个产物,命名与设计文档一致,没有 Linux、Intel Mac、Windows arm64、tray 产物。
- [ ] `monctl --version` 或等价方式能看到 tag 版本号。
- [ ] 在干净的 Windows 10/11 机器上:下载 GUI exe,能运行并识别显示器。
- [ ] 在干净的 macOS arm64 机器上:下载 dmg,拖入 Applications,按 README 放行后能运行并识别显示器。
- [ ] README 与实际行为一致。

---

## 后续可选增强(不在本次范围)

- 读取显示器 capabilities(VCP 0xF3),只显示真实支持的输入源,结果缓存到设置文件。
- 隐藏不用的输入源。
- 托盘常驻。
