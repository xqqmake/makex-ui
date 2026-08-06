# makex-ui 🚀

> 基于 Xray Core 构建的轻量级多协议代理管理面板

## ✨ 特性
- 支持 VMess / VLESS / Trojan / Shadowsocks / WireGuard 等多协议
- 可视化 Web 管理界面
- 一键安装 / 更新 / 卸载
- 支持 Docker 部署
- 多用户、流量统计、在线用户管理
- 支持 Reality / gRPC / WebSocket 等传输层

## 📦 快速开始

`ash
bash <(curl -Ls https://raw.githubusercontent.com/xqqmake/makex-ui/master/install.sh)
`

安装后输入 makex-ui 进入管理菜单。

## 🐳 Docker 部署

`ash
docker run -d --name makex-ui --restart always \
  -p 54321:54321 \
  -v /etc/makex-ui:/etc/makex-ui \
  xqqmake/makex-ui:latest
`

## 📄 协议

MIT License

## 🔗 相关链接
- 项目地址：https://github.com/xqqmake/makex-ui
- 问题反馈：https://github.com/xqqmake/makex-ui/issues
- 更新日志：https://github.com/xqqmake/makex-ui/releases
