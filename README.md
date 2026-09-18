# CPDC

Communication Plan Distribution Client，纯 Go 实现。一个进程可以通过 `cpdc_config.json` 同时承载多个不冲突的设备类型。

## 开发验证

```powershell
go test ./...
go vet ./...
```

重新生成 protobuf：

```powershell
.\build\scripts\generate-proto.ps1
```

生成五个发布文件（一个 Windows amd64 版本和四个 Linux 版本）：

```powershell
.\build\scripts\build.ps1
```

在Windows上也可以直接双击 `build\scripts\build.bat`，它会调用同目录的 `build.ps1`并在完成后保留窗口。

应用程序输出位于 `build/dist/bin/`，发布配置存放在 `build/dist/config/`，自启动脚本存放在 `build/dist/install/`。应用程序包括 Windows amd64 的 `CPDC.exe`以及 `CPDC-CCU`、`CPDC-MultibandRadio`、`CPDC-MultibandHandheld`和 `CPDC-HF`。所有发布文件均使用 `CGO_ENABLED=0`；只有CCU构建通过编译目标标记启用2倍工作空间预检，Windows版本使用普通1倍预检。

## 部署

1. 把发布文件、`cpdc_config.json` 和设备已有的 `ReReadJson.ini`（如适用）放在同一目录。
2. 配置 `deviceTypes`；`supportedTypes`只展示全部可选类型，不参与运行时选择。首次启动时保持 `esn` 为空，CPDC 会生成并原子保存固定 39 位十进制 ESN。
3. 默认使用全部“已启用、支持广播、非回环、非点对点”且具有 IPv4 地址的网卡；`--interface <网卡名>`仅用于诊断或限制为单张网卡。
4. CPDC 在 UDP 39001 的通配地址接收；每次逻辑回复从每张可用网卡各向 `255.255.255.255:39002` 发送一份，并只向 `127.0.0.1:39002` 发送一份回环副本。任一物理发送失败时重新枚举网卡、替换发送池并只重试原物理广播一次，回环不重发。

所有相对输出路径都以可执行文件所在目录为根，不使用进程当前工作目录。日志不记录 UDP 负载、JSON 正文、通信包正文、密钥或密码。

详细行为见 [CPDC 需求说明](docs/requirements/02-CPDC-requirements.md) 和 [交互需求](docs/requirements/03-CPDS-CPDC-interaction-requirements.md)。

## 启动
- 打包
.\build\scripts\generate-proto.ps1
.\build\scripts\build.ps1
- 启动时 加上参数 --config-path 路径，此路径是是 cpdc_config.json 路径，例如 
    - --config-path /data/user  (如果文件名是cpdc_config.json)
    - --config-path /data/user/cpdc-config-vehInte.json  (文件名称如果不是cpdc-config.json)
2、cpdc_config.json  文件中 有个ReReadjsonPath 字段，这个字段用来读取 ini 文件
