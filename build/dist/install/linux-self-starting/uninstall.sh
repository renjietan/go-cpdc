#!/bin/bash
#
# CPDC-CCU 服务卸载脚本（CentOS 6 SysVinit）
# 用法：sudo ./uninstall.sh
#

set -e

SERVICE_NAME="cpdc-ccu"
INSTALL_PATH="/home/TEDS/CPDC"
PID_FILE="$INSTALL_PATH/cpdc.pid"
LOCK_FILE="/var/lock/subsys/$SERVICE_NAME"
INIT_DST="/etc/init.d/$SERVICE_NAME"

# 检查 root 权限
if [ "$EUID" -ne 0 ]; then
    echo "Error: Please run as root (sudo)"
    exit 1
fi

echo "=== Uninstalling CPDC-CCU Service ==="

# 如果服务正在运行则停止
if service "$SERVICE_NAME" status >/dev/null 2>&1; then
    echo "Stopping service..."
    service "$SERVICE_NAME" stop
fi

# 禁用并删除 chkconfig 注册
echo "Disabling service..."
chkconfig "$SERVICE_NAME" off 2>/dev/null || true
chkconfig --del "$SERVICE_NAME" 2>/dev/null || true

# 删除 init.d 脚本
echo "Removing init.d script..."
rm -f "$INIT_DST"

# 清理残留文件
rm -f "$PID_FILE"
rm -f "$LOCK_FILE"

echo ""
echo "=== Uninstall Complete ==="
echo "Service has been removed and disabled."
echo "Program files remain in $INSTALL_PATH; remove manually if needed:"
echo "  rm -rf $INSTALL_PATH"
echo ""
