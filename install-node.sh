#!/bin/bash
# makex-ui 节点客户端安装脚本
# 支持 Linux/macOS/Windows(WSL)/Docker

set -e

# 颜色定义
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
NC='\033[0m'

# 默认值
MASTER_URL=""
NODE_UUID=""
NODE_TOKEN=""
INSTALL_DIR="/etc/makex-node"
SERVICE_NAME="makex-node"
COLLECT_INTERVAL=30
GITHUB_PROXY=""
DISABLE_REMOTE_CONTROL=false
DISABLE_AUTO_UPDATE=false
IGNORE_INSECURE_CERT=false
INCLUDE_BUFFER_MEMORY=false
GET_IP_FROM_NIC=false
ENABLE_DETAILED_GPU=false
MONITOR_ONLY_NIC=""
EXCLUDE_NIC=""
MONITOR_ONLY_MOUNT=""
MONTHLY_RESET_DAY=1

# 日志函数
log_info() { echo -e "${BLUE}[INFO]${NC} $1"; }
log_success() { echo -e "${GREEN}[OK]${NC} $1"; }
log_warn() { echo -e "${YELLOW}[WARN]${NC} $1"; }
log_error() { echo -e "${RED}[ERROR]${NC} $1"; }

# 显示帮助
show_help() {
    cat << EOF
makex-ui 节点客户端安装脚本

用法:
    bash install.sh [选项]

必需参数:
    --master URL          主控服务器地址 (如: https://192.168.1.1:33333/xui)
    --uuid UUID           节点UUID
    --token TOKEN         节点Token

可选参数:
    --install-dir DIR     安装目录 (默认: /etc/makex-node)
    --service-name NAME   服务名称 (默认: makex-node)
    --interval SEC        采集间隔秒数 (默认: 30)
    --github-proxy URL    GitHub代理地址

监控选项:
    --monitor-only-nic NIC    只监测特定网卡 (如: eth0)
    --exclude-nic NIC         排除特定网卡
    --monitor-only-mount DIR  只监测特定挂载点 (如: /)
    --monthly-reset-day DAY   网络统计月重置日 (默认: 1)

布尔选项:
    --disable-remote-control  禁用远程控制
    --disable-auto-update     禁用自动更新
    --ignore-insecure-cert    忽略不安全证书
    --include-buffer-memory   包含缓冲区内存
    --get-ip-from-nic         从网卡获取IP地址
    --enable-detailed-gpu     启用详细GPU监控

    --help                    显示此帮助信息

示例:
    bash install.sh --master https://192.168.1.1:33333/xui --uuid xxx --token yyy
    bash install.sh --master https://example.com --uuid xxx --token yyy --interval 60 --monitor-only-nic eth0
EOF
}

# 解析参数
while [[ $# -gt 0 ]]; do
    case $1 in
        --master)
            MASTER_URL="$2"
            shift 2
            ;;
        --uuid)
            NODE_UUID="$2"
            shift 2
            ;;
        --token)
            NODE_TOKEN="$2"
            shift 2
            ;;
        --install-dir)
            INSTALL_DIR="$2"
            shift 2
            ;;
        --service-name)
            SERVICE_NAME="$2"
            shift 2
            ;;
        --interval)
            COLLECT_INTERVAL="$2"
            shift 2
            ;;
        --github-proxy)
            GITHUB_PROXY="$2"
            shift 2
            ;;
        --monitor-only-nic)
            MONITOR_ONLY_NIC="$2"
            shift 2
            ;;
        --exclude-nic)
            EXCLUDE_NIC="$2"
            shift 2
            ;;
        --monitor-only-mount)
            MONITOR_ONLY_MOUNT="$2"
            shift 2
            ;;
        --monthly-reset-day)
            MONTHLY_RESET_DAY="$2"
            shift 2
            ;;
        --disable-remote-control)
            DISABLE_REMOTE_CONTROL=true
            shift
            ;;
        --disable-auto-update)
            DISABLE_AUTO_UPDATE=true
            shift
            ;;
        --ignore-insecure-cert)
            IGNORE_INSECURE_CERT=true
            shift
            ;;
        --include-buffer-memory)
            INCLUDE_BUFFER_MEMORY=true
            shift
            ;;
        --get-ip-from-nic)
            GET_IP_FROM_NIC=true
            shift
            ;;
        --enable-detailed-gpu)
            ENABLE_DETAILED_GPU=true
            shift
            ;;
        --help)
            show_help
            exit 0
            ;;
        *)
            log_error "未知参数: $1"
            show_help
            exit 1
            ;;
    esac
done

# 验证必需参数
if [ -z "$MASTER_URL" ] || [ -z "$NODE_UUID" ] || [ -z "$NODE_TOKEN" ]; then
    log_error "缺少必需参数: --master, --uuid, --token"
    show_help
    exit 1
fi

# 移除末尾斜杠
MASTER_URL="${MASTER_URL%/}"

# 检测操作系统
detect_os() {
    local os_type=$(uname -s)
    case $os_type in
        Linux*)
            echo "linux"
            ;;
        Darwin*)
            echo "macos"
            ;;
        MINGW*|MSYS*|CYGWIN*)
            echo "windows"
            ;;
        *)
            echo "unknown"
            ;;
    esac
}

OS_TYPE=$(detect_os)

# 显示安装信息
echo -e "${CYAN}===========================================${NC}"
echo -e "${CYAN}  makex-ui 节点客户端安装${NC}"
echo -e "${CYAN}===========================================${NC}"
echo ""
log_info "操作系统: $OS_TYPE"
log_info "主控地址: $MASTER_URL"
log_info "节点UUID: ${NODE_UUID:0:8}..."
log_info "安装目录: $INSTALL_DIR"
log_info "服务名称: $SERVICE_NAME"
log_info "采集间隔: ${COLLECT_INTERVAL}秒"
echo ""

# 创建安装目录
mkdir -p "$INSTALL_DIR"

# 保存配置
save_config() {
    cat > "$INSTALL_DIR/config" << EOF
MASTER_URL=$MASTER_URL
NODE_UUID=$NODE_UUID
NODE_TOKEN=$NODE_TOKEN
SERVICE_NAME=$SERVICE_NAME
COLLECT_INTERVAL=$COLLECT_INTERVAL
GITHUB_PROXY=$GITHUB_PROXY
DISABLE_REMOTE_CONTROL=$DISABLE_REMOTE_CONTROL
DISABLE_AUTO_UPDATE=$DISABLE_AUTO_UPDATE
IGNORE_INSECURE_CERT=$IGNORE_INSECURE_CERT
INCLUDE_BUFFER_MEMORY=$INCLUDE_BUFFER_MEMORY
GET_IP_FROM_NIC=$GET_IP_FROM_NIC
ENABLE_DETAILED_GPU=$ENABLE_DETAILED_GPU
MONITOR_ONLY_NIC=$MONITOR_ONLY_NIC
EXCLUDE_NIC=$EXCLUDE_NIC
MONITOR_ONLY_MOUNT=$MONITOR_ONLY_MOUNT
MONTHLY_RESET_DAY=$MONTHLY_RESET_DAY
EOF
    log_success "配置已保存到 $INSTALL_DIR/config"
}

# 获取系统信息
get_system_info() {
    # OS
    if [ -f /etc/os-release ]; then
        . /etc/os-release
        OS_NAME="$PRETTY_NAME"
    else
        OS_NAME=$(uname -s)
    fi
    
    # Architecture
    ARCH=$(uname -m)
    
    # Kernel
    KERNEL=$(uname -r)
    
    # CPU
    if [ -f /proc/cpuinfo ]; then
        CPU_NAME=$(grep "model name" /proc/cpuinfo | head -1 | cut -d: -f2 | xargs)
        CPU_CORES=$(nproc)
    else
        CPU_NAME=$(sysctl -n machdep.cpu.brand_string 2>/dev/null || echo "Unknown")
        CPU_CORES=$(sysctl -n hw.ncpu 2>/dev/null || echo 1)
    fi
    
    # Memory
    if [ -f /proc/meminfo ]; then
        MEM_TOTAL=$(grep MemTotal /proc/meminfo | awk '{print $2}')
        MEM_TOTAL=$((MEM_TOTAL * 1024))
    else
        MEM_TOTAL=$(sysctl -n hw.memsize 2>/dev/null || echo 0)
    fi
    
    # Disk
    DISK_TOTAL=$(df -B1 / | tail -1 | awk '{print $2}')
    
    # IP
    if [ "$GET_IP_FROM_NIC" = "true" ]; then
        IPV4=$(ip -4 addr show scope global 2>/dev/null | grep -oP '(?<=inet\s)\d+(\.\d+){3}' | head -1)
    else
        IPV4=$(curl -s --connect-timeout 5 ifconfig.me 2>/dev/null || \
                curl -s --connect-timeout 5 api.ipify.org 2>/dev/null || \
                ip -4 addr show scope global 2>/dev/null | grep -oP '(?<=inet\s)\d+(\.\d+){3}' | head -1)
    fi
}

# 获取实时数据
get_realtime_data() {
    # CPU使用率
    if [ -f /proc/stat ]; then
        CPU_IDLE=$(top -bn2 -d1 | grep "Cpu(s)" | tail -1 | awk '{print $8}' | cut -d. -f1)
        [ -z "$CPU_IDLE" ] && CPU_IDLE=$(vmstat 1 2 | tail -1 | awk '{print $15}')
        CPU_USAGE=$((100 - ${CPU_IDLE:-0}))
    else
        CPU_USAGE=0
    fi
    
    # 内存使用
    if [ -f /proc/meminfo ]; then
        MEM_AVAIL=$(grep MemAvailable /proc/meminfo 2>/dev/null | awk '{print $2}')
        [ -z "$MEM_AVAIL" ] && MEM_AVAIL=$(grep MemFree /proc/meminfo | awk '{print $2}')
        MEM_USED=$((MEM_TOTAL/1024 - MEM_AVAIL))
        MEM_USED=$((MEM_USED * 1024))
        
        # Swap
        SWAP_TOTAL=$(grep SwapTotal /proc/meminfo | awk '{print $2}')
        SWAP_FREE=$(grep SwapFree /proc/meminfo | awk '{print $2}')
        SWAP_USED=$((SWAP_TOTAL - SWAP_FREE))
        SWAP_TOTAL=$((SWAP_TOTAL * 1024))
        SWAP_USED=$((SWAP_USED * 1024))
    else
        MEM_USED=0
        SWAP_TOTAL=0
        SWAP_USED=0
    fi
    
    # 磁盘使用
    DISK_USED=$(df -B1 / | tail -1 | awk '{print $3}')
    
    # 网络流量
    if [ -f /proc/net/dev ]; then
        if [ -n "$MONITOR_ONLY_NIC" ]; then
            NET_IN=$(cat /proc/net/dev | grep "$MONITOR_ONLY_NIC" | awk '{print $2}')
            NET_OUT=$(cat /proc/net/dev | grep "$MONITOR_ONLY_NIC" | awk '{print $10}')
        else
            NET_IN=$(cat /proc/net/dev | tail -n +3 | awk '{sum+=$2} END {print sum}')
            NET_OUT=$(cat /proc/net/dev | tail -n +3 | awk '{sum+=$10} END {print sum}')
        fi
    else
        NET_IN=0
        NET_OUT=0
    fi
    
    # 负载
    if [ -f /proc/loadavg ]; then
        LOAD1=$(cut -d' ' -f1 /proc/loadavg)
        LOAD5=$(cut -d' ' -f2 /proc/loadavg)
        LOAD15=$(cut -d' ' -f3 /proc/loadavg)
    else
        LOAD1=0
        LOAD5=0
        LOAD15=0
    fi
    
    # 运行时间
    if [ -f /proc/uptime ]; then
        UPTIME=$(cut -d' ' -f1 /proc/uptime | cut -d. -f1)
    else
        UPTIME=0
    fi
    
    # 进程数
    PROCESS_COUNT=$(ps aux 2>/dev/null | wc -l || echo 0)
    
    # 连接数
    if command -v ss &>/dev/null; then
        TCP_COUNT=$(ss -t 2>/dev/null | tail -n +2 | wc -l || echo 0)
        UDP_COUNT=$(ss -u 2>/dev/null | tail -n +2 | wc -l || echo 0)
    else
        TCP_COUNT=0
        UDP_COUNT=0
    fi
}

# 上报数据
report_data() {
    get_system_info
    get_realtime_data
    
    local data=$(cat << EOF
{
    "uuid": "$NODE_UUID",
    "token": "$NODE_TOKEN",
    "os": "$OS_NAME",
    "arch": "$ARCH",
    "kernelVersion": "$KERNEL",
    "cpuName": "$CPU_NAME",
    "cpuCores": $CPU_CORES,
    "memTotal": $MEM_TOTAL,
    "diskTotal": $DISK_TOTAL,
    "ipv4": "$IPV4",
    "cpuUsage": $CPU_USAGE,
    "memUsed": $MEM_USED,
    "swapTotal": $SWAP_TOTAL,
    "swapUsed": $SWAP_USED,
    "diskUsed": $DISK_USED,
    "netIn": $NET_IN,
    "netOut": $NET_OUT,
    "load1": $LOAD1,
    "load5": $LOAD5,
    "load15": $LOAD15,
    "uptime": $UPTIME,
    "processCount": $PROCESS_COUNT,
    "tcpCount": $TCP_COUNT,
    "udpCount": $UDP_COUNT
}
EOF
)
    
    local curl_opts="-sk"
    if [ "$IGNORE_INSECURE_CERT" = "true" ]; then
        curl_opts="-sk --insecure"
    fi
    
    local result=$(curl $curl_opts -X POST "$MASTER_URL/panel/api/nodes/report" \
        -H 'Content-Type: application/json' \
        -d "$data" \
        --connect-timeout 10 --max-time 15 2>/dev/null)
    
    if echo "$result" | grep -q '"success":true'; then
        return 0
    else
        return 1
    fi
}

# 创建上报脚本
create_report_script() {
    cat > "$INSTALL_DIR/report.sh" << 'REPORT_EOF'
#!/bin/bash
source /etc/makex-node/config 2>/dev/null || source "$(dirname "$0")/config"

# 获取系统信息
if [ -f /etc/os-release ]; then
    . /etc/os-release
    OS_NAME="$PRETTY_NAME"
else
    OS_NAME=$(uname -s)
fi
ARCH=$(uname -m)
KERNEL=$(uname -r)

if [ -f /proc/cpuinfo ]; then
    CPU_NAME=$(grep "model name" /proc/cpuinfo | head -1 | cut -d: -f2 | xargs)
    CPU_CORES=$(nproc)
else
    CPU_NAME=$(sysctl -n machdep.cpu.brand_string 2>/dev/null || echo "Unknown")
    CPU_CORES=$(sysctl -n hw.ncpu 2>/dev/null || echo 1)
fi

if [ -f /proc/meminfo ]; then
    MEM_TOTAL_KB=$(grep MemTotal /proc/meminfo | awk '{print $2}')
    MEM_TOTAL=$((MEM_TOTAL_KB * 1024))
    MEM_AVAIL=$(grep MemAvailable /proc/meminfo 2>/dev/null | awk '{print $2}')
    [ -z "$MEM_AVAIL" ] && MEM_AVAIL=$(grep MemFree /proc/meminfo | awk '{print $2}')
    MEM_USED=$((MEM_TOTAL_KB - MEM_AVAIL))
    MEM_USED=$((MEM_USED * 1024))
    
    SWAP_TOTAL_KB=$(grep SwapTotal /proc/meminfo | awk '{print $2}')
    SWAP_FREE_KB=$(grep SwapFree /proc/meminfo | awk '{print $2}')
    SWAP_TOTAL=$((SWAP_TOTAL_KB * 1024))
    SWAP_USED=$(((SWAP_TOTAL_KB - SWAP_FREE_KB) * 1024))
else
    MEM_TOTAL=$(sysctl -n hw.memsize 2>/dev/null || echo 0)
    MEM_USED=0
    SWAP_TOTAL=0
    SWAP_USED=0
fi

DISK_TOTAL=$(df -B1 / | tail -1 | awk '{print $2}')
DISK_USED=$(df -B1 / | tail -1 | awk '{print $3}')

if [ "$GET_IP_FROM_NIC" = "true" ]; then
    IPV4=$(ip -4 addr show scope global 2>/dev/null | grep -oP '(?<=inet\s)\d+(\.\d+){3}' | head -1)
else
    IPV4=$(curl -s --connect-timeout 5 ifconfig.me 2>/dev/null || echo "")
fi

CPU_IDLE=$(top -bn2 -d1 | grep "Cpu(s)" | tail -1 | awk '{print $8}' | cut -d. -f1)
[ -z "$CPU_IDLE" ] && CPU_IDLE=$(vmstat 1 2 | tail -1 | awk '{print $15}')
CPU_USAGE=$((100 - ${CPU_IDLE:-0}))

if [ -n "$MONITOR_ONLY_NIC" ]; then
    NET_IN=$(cat /proc/net/dev | grep "$MONITOR_ONLY_NIC" | awk '{print $2}')
    NET_OUT=$(cat /proc/net/dev | grep "$MONITOR_ONLY_NIC" | awk '{print $10}')
else
    NET_IN=$(cat /proc/net/dev | tail -n +3 | awk '{sum+=$2} END {print sum}')
    NET_OUT=$(cat /proc/net/dev | tail -n +3 | awk '{sum+=$10} END {print sum}')
fi

LOAD1=$(cut -d' ' -f1 /proc/loadavg 2>/dev/null || echo 0)
LOAD5=$(cut -d' ' -f2 /proc/loadavg 2>/dev/null || echo 0)
LOAD15=$(cut -d' ' -f3 /proc/loadavg 2>/dev/null || echo 0)
UPTIME=$(cut -d' ' -f1 /proc/uptime 2>/dev/null | cut -d. -f1 || echo 0)
PROCESS_COUNT=$(ps aux 2>/dev/null | wc -l || echo 0)
TCP_COUNT=$(ss -t 2>/dev/null | tail -n +2 | wc -l || echo 0)
UDP_COUNT=$(ss -u 2>/dev/null | tail -n +2 | wc -l || echo 0)

CURL_OPTS="-sk"
[ "$IGNORE_INSECURE_CERT" = "true" ] && CURL_OPTS="-sk --insecure"

curl $CURL_OPTS -X POST "$MASTER_URL/panel/api/nodes/report" \
    -H 'Content-Type: application/json' \
    -d "{
        \"uuid\":\"$NODE_UUID\",
        \"token\":\"$NODE_TOKEN\",
        \"os\":\"$OS_NAME\",
        \"arch\":\"$ARCH\",
        \"kernelVersion\":\"$KERNEL\",
        \"cpuName\":\"$CPU_NAME\",
        \"cpuCores\":$CPU_CORES,
        \"memTotal\":$MEM_TOTAL,
        \"diskTotal\":$DISK_TOTAL,
        \"ipv4\":\"$IPV4\",
        \"cpuUsage\":$CPU_USAGE,
        \"memUsed\":$MEM_USED,
        \"swapTotal\":$SWAP_TOTAL,
        \"swapUsed\":$SWAP_USED,
        \"diskUsed\":$DISK_USED,
        \"netIn\":$NET_IN,
        \"netOut\":$NET_OUT,
        \"load1\":$LOAD1,
        \"load5\":$LOAD5,
        \"load15\":$LOAD15,
        \"uptime\":$UPTIME,
        \"processCount\":$PROCESS_COUNT,
        \"tcpCount\":$TCP_COUNT,
        \"udpCount\":$UDP_COUNT
    }" \
    --connect-timeout 10 --max-time 15 2>/dev/null
REPORT_EOF

    chmod +x "$INSTALL_DIR/report.sh"
    log_success "上报脚本已创建"
}

# 创建systemd服务
create_systemd_service() {
    cat > /etc/systemd/system/${SERVICE_NAME}.service << EOF
[Unit]
Description=makex-ui Node Client
After=network.target

[Service]
Type=oneshot
ExecStart=${INSTALL_DIR}/report.sh
EOF

    cat > /etc/systemd/system/${SERVICE_NAME}.timer << EOF
[Unit]
Description=makex-ui Node Client Timer

[Timer]
OnBootSec=30s
OnUnitActiveSec=${COLLECT_INTERVAL}s
AccuracySec=5s

[Install]
WantedBy=timers.target
EOF

    systemctl daemon-reload
    systemctl enable ${SERVICE_NAME}.timer
    systemctl start ${SERVICE_NAME}.timer
    
    log_success "systemd服务已创建并启动"
}

# 创建macOS launchd服务
create_macos_service() {
    local plist_path="/Library/LaunchDaemons/com.makex.${SERVICE_NAME}.plist"
    
    cat > "$plist_path" << EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.makex.${SERVICE_NAME}</string>
    <key>ProgramArguments</key>
    <array>
        <string>${INSTALL_DIR}/report.sh</string>
    </array>
    <key>StartInterval</key>
    <integer>${COLLECT_INTERVAL}</integer>
    <key>RunAtLoad</key>
    <true/>
</dict>
</plist>
EOF

    launchctl load "$plist_path"
    log_success "macOS launchd服务已创建并启动"
}

# 安装流程
main() {
    echo -e "${BLUE}[1/5]${NC} 保存配置..."
    save_config
    
    echo -e "${BLUE}[2/5]${NC} 创建上报脚本..."
    create_report_script
    
    echo -e "${BLUE}[3/5]${NC} 配置系统服务..."
    case $OS_TYPE in
        linux)
            create_systemd_service
            ;;
        macos)
            create_macos_service
            ;;
        *)
            log_warn "不支持的服务类型，请手动配置定时任务"
            ;;
    esac
    
    echo -e "${BLUE}[4/5]${NC} 执行首次上报..."
    if report_data; then
        log_success "首次上报成功"
    else
        log_warn "首次上报失败，请检查网络连接"
    fi
    
    echo -e "${BLUE}[5/5]${NC} 安装完成！"
    echo ""
    echo -e "${CYAN}===========================================${NC}"
    echo -e "${GREEN}  安装完成！${NC}"
    echo -e "${CYAN}===========================================${NC}"
    echo ""
    echo -e "管理命令:"
    echo -e "  查看状态: systemctl status ${SERVICE_NAME}.timer"
    echo -e "  停止上报: systemctl stop ${SERVICE_NAME}.timer"
    echo -e "  手动上报: bash ${INSTALL_DIR}/report.sh"
    echo -e "  卸载: systemctl disable --now ${SERVICE_NAME}.timer && rm -rf ${INSTALL_DIR}"
    echo ""
}

main
