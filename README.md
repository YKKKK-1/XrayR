# XrayR

[![](https://img.shields.io/badge/TgChat-@XrayR讨论-blue.svg)](https://t.me/XrayR_project)
[![](https://img.shields.io/badge/Channel-@XrayR通知-blue.svg)](https://t.me/XrayR_channel)
![](https://img.shields.io/github/stars/XrayR-project/XrayR)
![](https://img.shields.io/github/forks/XrayR-project/XrayR)
![](https://github.com/XrayR-project/XrayR/actions/workflows/release.yml/badge.svg)
![](https://github.com/XrayR-project/XrayR/actions/workflows/docker.yml/badge.svg)
[![Github All Releases](https://img.shields.io/github/downloads/XrayR-project/XrayR/total.svg)]()


[English](https://github.com/XrayR-project/XrayR/blob/master/README-en.md)|[Iranian](https://github.com/XrayR-project/XrayR/blob/master/README_Fa.md)|[Vietnamese](https://github.com/XrayR-project/XrayR/blob/master/README-vi.md)

A Xray backend framework that can easily support many panels.

一个基于Xray的后端框架，支持V2ay,Trojan,Shadowsocks协议，极易扩展，支持多面板对接。

如果您喜欢本项目，可以右上角点个star+watch，持续关注本项目的进展。

使用教程：[详细使用教程](https://xrayr-project.github.io/XrayR-doc/)


## 免责声明

本项目只是本人个人学习开发并维护，本人不保证任何可用性，也不对使用本软件造成的任何后果负责。

## 特点

* 永久开源且免费。
* 支持V2ray，Trojan， Shadowsocks多种协议。
* 支持Vless和XTLS等新特性。
* 支持单实例对接多面板、多节点，无需重复启动。
* 支持限制在线IP
* 支持节点端口级别、用户级别限速。
* 配置简单明了。
* 修改配置自动重启实例。
* 方便编译和升级，可以快速更新核心版本， 支持Xray-core新特性。

## 功能介绍

| 功能        | v2ray | trojan | shadowsocks |
|-----------|-------|--------|-------------|
| 获取节点信息    | √     | √      | √           |
| 获取用户信息    | √     | √      | √           |
| 用户流量统计    | √     | √      | √           |
| 服务器信息上报   | √     | √      | √           |
| 自动申请tls证书 | √     | √      | √           |
| 自动续签tls证书 | √     | √      | √           |
| 在线人数统计    | √     | √      | √           |
| 在线用户限制    | √     | √      | √           |
| 审计规则      | √     | √      | √           |
| 节点端口限速    | √     | √      | √           |
| 按照用户限速    | √     | √      | √           |
| 自定义DNS    | √     | √      | √           |

## 支持前端

| 前端                                                     | v2ray | trojan | shadowsocks             |
|--------------------------------------------------------|-------|--------|-------------------------|
| sspanel-uim                                            | √     | √      | √ (单端口多用户和V2ray-Plugin) |
| v2board                                                | √     | √      | √                       |
| [PMPanel](https://github.com/ByteInternetHK/PMPanel)   | √     | √      | √                       |
| [ProxyPanel](https://github.com/ProxyPanel/ProxyPanel) | √     | √      | √                       |
| [WHMCS (V2RaySocks)](https://v2raysocks.doxtex.com/)   | √     | √      | √                       |
| [GoV2Panel](https://github.com/pingProMax/gov2panel)   | √     | √      | √                       |
| [BunPanel](https://github.com/pennyMorant/bunpanel-release)   | √     | √      | √                       |

## 本分支的内核与兼容性

本分支使用 Xray-core v26.11.9 和 Go 1.27。运行 `XrayR version` 可查看二进制实际嵌入的内核版本。Go 模块版本末尾的 `676eaf66f8f0` 对应该正式版的发布提交。[上游发布](https://github.com/XTLS/Xray-core/releases/tag/v26.11.9)

- Vision 包含上游针对 direct copy 后外层 TLS 关闭通知的修复。客户端也使用 Xray 的节点可在两端升级后验证连接收尾。[修复记录](https://github.com/XTLS/Xray-core/pull/6834)
- 使用自定义出站时，请检查 Freedom 对私有目标地址的默认限制，以及 TLS 配置中的旧 `allowInsecure` 字段。确需访问内网服务时，可用 `finalRules` 限定允许的地址和端口。
- 旧 Shadowsocks 的 `none` / `plain` 加密方式和 `DisableIVCheck` 开关已被内核移除。使用旧式 mKCP 包头或 seed 的自定义配置也需要迁移到新版内核支持的设置。
- Mihomo 连接标准版 VLESS + REALITY 节点时，在节点上设置 `client-fingerprint: chrome`，并在已有 `reality-opts` 中设置 `support-x25519mlkem768: true`。使用订阅时也应在该节点的 Mihomo 订阅模板中保留此选项。[Mihomo 配置说明](https://wiki.metacubex.one/config/proxies/tls/#reality-optssupport-x25519mlkem768)
- 需要接入只发送传统 X25519 密钥份额的客户端时，可用 `GOOS=linux GOARCH=amd64 bash release/compat/build.sh ./XrayR-linux-amd64-compat` 单独编译兼容版。`./XrayR-linux-amd64-compat version` 会显示 `XrayR-REALITY-X25519-compat`。该程序的全部 REALITY 入站均接受通过身份校验的传统 X25519 握手，这些连接使用经典密钥交换。
- 本地可用 `go test ./panel -run '^TestVision'` 检查 TLS、REALITY、流量计数和限速，用 `go test ./panel -run '^$' -bench BenchmarkVisionRoundTrip -benchmem -count 5` 测量回环双向传输吞吐。

## 软件安装

### 一键安装

```
wget -N https://raw.githubusercontent.com/XrayR-project/XrayR-release/master/install.sh && bash install.sh
```

### 使用Docker部署软件

[Docker部署教程](https://xrayr-project.github.io/XrayR-doc/xrayr-xia-zai-he-an-zhuang/install/docker)

### 手动安装

[手动安装教程](https://xrayr-project.github.io/XrayR-doc/xrayr-xia-zai-he-an-zhuang/install/manual)

## 配置文件及详细使用教程

[详细使用教程](https://xrayr-project.github.io/XrayR-doc/)

## Thanks

* [Project X](https://github.com/XTLS/)
* [V2Fly](https://github.com/v2fly)
* [VNet-V2ray](https://github.com/ProxyPanel/VNet-V2ray)
* [Air-Universe](https://github.com/crossfw/Air-Universe)

## Licence

[Mozilla Public License Version 2.0](https://github.com/XrayR-project/XrayR/blob/master/LICENSE)

## Telgram

[XrayR后端讨论](https://t.me/XrayR_project)

[XrayR通知](https://t.me/XrayR_channel)

## Stargazers over time

[![Stargazers over time](https://starchart.cc/XrayR-project/XrayR.svg)](https://starchart.cc/XrayR-project/XrayR)


