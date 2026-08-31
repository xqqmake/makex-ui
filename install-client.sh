#!/bin/bash
# makex-ui 节点客户端安装脚本
# 用法: bash install-client.sh <主控地址> <节点名称> <分组>

set -e

# 参数检查
MASTER_URL="${1:-}"
NODE_NAME="${2:-$(hostname)}"
NODE_GROUP="${3:-default}"

if [ -z "$MASTER_URL" ]; then
    echo "用法: bash install-client.sh <主控地址> [节点名称] [分组]"
    echo "示例: bash install-client.sh https://198.46.252.28:33333/xui 香港-01 花屿云"
    exit 1
fi

# 去掉末尾斜杠
MASTER_URL="${MASTER_URL%/}"

echo "=========================================="
echo "  makex-ui 节点客户端安装"
echo "=========================================="
echo "主控地址: $MASTER_URL"
echo "节点名称: $NODE_NAME"
echo "分组: $NODE_GROUP"
echo "=========================================="

# 创建目录
INSTALL_DIR="/etc/makex-node"
mkdir -p "$INSTALL_DIR"

# 从主控服务器获取 Token
echo "[1/5] 从主控服务器获取节点 Token..."

# 先尝试在主控创建节点
REGISTER_RESULT=$(curl -sk -X POST "$MASTER_URL/panel/api/nodes/create" \
    -H 'Content-Type: application/json' \
    -d "{\"name\":\"$NODE_NAME\",\"host\":\"$(curl -s ifconfig.me 2>/dev/null || hostname -I | awk '{print $1}')\",\"port\":22,\"group\":\"$NODE_GROUP\",\"remark\":\"$(uname -n)\"}" 2>/dev/null)

NODE_UUID=$(echo "$REGISTER_RESULT" | grep -o '"uuid":"[^"]*"' | head -1 | cut -d'"' -f4)
NODE_TOKEN=$(echo "$REGISTER_RESULT" | grep -o '"token":"[^"]*"' | head -1 | cut -d'"' -f4)

if [ -z "$NODE_UUID" ] || [ -z "$NODE_TOKEN" ]; then
    echo "错误: 无法从主控获取 Token"
    echo "返回: $REGISTER_RESULT"
    exit 1
fi

echo "UUID: ${NODE_UUID:0:8}..."
echo "Token: ${NODE_TOKEN:0:8}..."

# 保存配置
cat > "$INSTALL_DIR/config" << EOF
MASTER_URL=$MASTER_URL
NODE_UUID=$NODE_UUID
NODE_TOKEN=$NODE_TOKEN
NODE_NAME=$NODE_NAME
NODE_GROUP=$NODE_GROUP
EOF

echo "[2/5] 配置已保存到 $INSTALL_DIR/config"

# 创建上报脚本
cat > "$INSTALL_DIR/report.sh" << 'REPORT_EOF'
#!/bin/bash
# 节点状态上报脚本

CONFIG="/etc/makex-node/config"
if [ ! -f "$CONFIG" ]; then
    echo "配置文件不存在: $CONFIG"
    exit 1
fi

source "$CONFIG"

# 采集系统信息
OS=$(cat /etc/os-release 2>/dev/null | grep PRETTY_NAME | cut -d'"' -f2 || uname -s)
ARCH=$(uname -m)
HOSTNAME=$(uname -n)
UPTIME=$(cat /proc/uptime | awk '{print int($1)}')

# CPU 信息
CPU_NAME=$(grep "model name" /proc/cpuinfo 2>/dev/null | head -1 | cut -d: -f2 | xargs || echo "Unknown")
CPU_CORES=$(nproc)

# CPU 使用率 (采样1秒)
CPU_IDLE=$(top -bn2 -d1 | grep "Cpu(s)" | tail -1 | awk '{print $8}' | cut -d. -f1)
if [ -z "$CPU_IDLE" ]; then
    CPU_IDLE=$(vmstat 1 2 | tail -1 | awk '{print $15}')
fi
CPU_USAGE=$((100 - ${CPU_IDLE:-0}))

# 内存信息
MEM_TOTAL=$(grep MemTotal /proc/meminfo | awk '{print $2}')
MEM_AVAIL=$(grep MemAvailable /proc/meminfo 2>/dev/null | awk '{print $2}')
if [ -z "$MEM_AVAIL" ]; then
    MEM_AVAIL=$(grep MemFree /proc/meminfo | awk '{print $2}')
fi
MEM_USED=$((MEM_TOTAL - MEM_AVAIL))
# 转换为字节
MEM_TOTAL_B=$((MEM_TOTAL * 1024))
MEM_USED_B=$((MEM_USED * 1024))

# 磁盘信息 (根分区)
DISK_TOTAL=$(df -B1 / | tail -1 | awk '{print $2}')
DISK_USED=$(df -B1 / | tail -1 | awk '{print $3}')

# 网络信息
NET_RX=$(cat /proc/net/dev | grep -E "eth|ens|enp" | head -1 | awk '{print $2}')
NET_TX=$(cat /proc/net/dev | grep -E "eth|ens|enp" | head -1 | awk '{print $10}')

# IPv4/IPv6
IPV4=$(curl -s --connect-timeout 3 ifconfig.me 2>/dev/null || echo "")
IPV6=$(curl -s --connect-timeout 3 -6 ifconfig.co 2>/dev/null || echo "")

# 负载
LOAD_1=$(cat /proc/loadavg | awk '{print $1}')
LOAD_5=$(cat /proc/loadavg | awk '{print $2}')
LOAD_15=$(cat /proc/loadavg | awk '{print $3}')

# 构建 JSON
REPORT_JSON=$(cat << JSON_EOF
{
    "uuid": "$NODE_UUID",
    "token": "$NODE_TOKEN",
    "os": "$OS",
    "arch": "$ARCH",
    "cpuName": "$CPU_NAME",
    "cpuCores": $CPU_CORES,
    "memTotal": $MEM_TOTAL_B,
    "diskTotal": $DISK_TOTAL,
    "cpuUsage": $CPU_USAGE,
    "memUsed": $MEM_USED_B,
    "diskUsed": $DISK_USED,
    "netRx": ${NET_RX:-0},
    "netTx": ${NET_TX:-0},
    "ipv4": "$IPV4",
    "ipv6": "$IPV6",
    "load1": $LOAD_1,
    "load5": $LOAD_5,
    "load15": $LOAD_15,
    "uptime": $UPTIME
}
JSON_EOF
)

# 上报
RESULT=$(curl -sk -X POST "$MASTER_URL/panel/api/nodes/report" \
    -H 'Content-Type: application/json' \
    -d "$REPORT_JSON" \
    --connect-timeout 10 \
    --max-time 15 2>/dev/null)

echo "[$(date '+%Y-%m-%d %H:%M:%S')] 上报完成: $RESULT"
REPORT_EOF

chmod +x "$INSTALL_DIR/report.sh"
echo "[3/5] 上报脚本已创建"

# 创建 systemd 服务
cat > /etc/systemd/system/makex-node.service << EOF
[Unit]
Description=makex-ui Node Client
After=network.target

[Service]
Type=oneshot
ExecStart=/etc/makex-node/report.sh
EOF

cat > /etc/systemd/system/makex-node.timer << EOF
[Unit]
Description=makex-ui Node Client Timer

[Timer]
OnBootSec=30s
OnUnitActiveSec=30s
AccuracySec=5s

[Install]
WantedBy=timers.target
EOF

echo "[4/5] systemd 服务已创建"

# 启用并启动
systemctl daemon-reload
systemctl enable makex-node.timer
systemctl start makex-node.timer

echo "[5/5] 服务已启动"

# 立即执行一次上报
echo ""
echo "正在执行首次上报..."
bash "$INSTALL_DIR/report.sh"

echo ""
echo "=========================================="
echo "  安装完成！"
echo "=========================================="
echo "配置文件: $INSTALL_DIR/config"
echo "上报脚本: $INSTALL_DIR/report.sh"
echo "上报间隔: 30秒"
echo ""
echo "管理命令:"
echo "  查看状态: systemctl status makex-node.timer"
echo "  停止上报: systemctl stop makex-node.timer"
echo "  手动上报: bash $INSTALL_DIR/report.sh"
echo "  卸载: systemctl disable --now makex-node.timer && rm -rf $INSTALL_DIR /etc/systemd/system/makex-node.*"
echo "=========================================="
