#!/bin/bash
#
# CPDC-CCU 服务安装脚本（CentOS 6 SysVinit）
# 用法：sudo ./install.sh
#

set -e

SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd)
INSTALL_PATH="/home/TEDS/CPDC"
SERVICE_NAME="cpdc-ccu"
INIT_SRC="$SCRIPT_DIR/cpdc-ccu.init.d"
INIT_DST="/etc/init.d/$SERVICE_NAME"

# 检查 root 权限
if [ "$EUID" -ne 0 ]; then
    echo "Error: Please run as root (sudo)"
    exit 1
fi

# 检查 init 脚本是否存在
if [ ! -f "$INIT_SRC" ]; then
    echo "Error: init script not found: $INIT_SRC"
    exit 1
fi

echo "=== Installing CPDC-CCU Service ==="

# 安装 init.d 脚本
echo "Installing init.d script..."
cp "$INIT_SRC" "$INIT_DST"
chmod +x "$INIT_DST"

# 如果当前目录不是安装目录，则复制程序文件
if [ "$SCRIPT_DIR" != "$INSTALL_PATH" ]; then
    echo "Copying program files to $INSTALL_PATH..."
    mkdir -p "$INSTALL_PATH"
    cp "$SCRIPT_DIR/CPDC-CCU" "$INSTALL_PATH/CPDC-CCU"
    cp "$SCRIPT_DIR/start.sh" "$INSTALL_PATH/start.sh"
    cp "$SCRIPT_DIR/stop.sh" "$INSTALL_PATH/stop.sh"
    cp "$SCRIPT_DIR/restart.sh" "$INSTALL_PATH/restart.sh"
    cp "$SCRIPT_DIR/cpdc_config.json" "$INSTALL_PATH/cpdc_config.json"
fi

# 确保可执行
echo "Ensuring executables..."
chmod +x "$INSTALL_PATH/CPDC-CCU"
chmod +x "$INSTALL_PATH/start.sh"
chmod +x "$INSTALL_PATH/stop.sh"
chmod +x "$INSTALL_PATH/restart.sh"

# 注册到 chkconfig
echo "Registering with chkconfig..."
chkconfig --del "$SERVICE_NAME" 2>/dev/null || true
chkconfig --add "$SERVICE_NAME"
chkconfig "$SERVICE_NAME" on

echo ""
echo "=== Installation Complete ==="
echo ""
echo "Service installed. Available commands:"
echo "  Start:   service $SERVICE_NAME start"
echo "  Stop:    service $SERVICE_NAME stop"
echo "  Status:  service $SERVICE_NAME status"
echo "  Restart: service $SERVICE_NAME restart"
echo "  Logs:    tail -f $INSTALL_PATH/cpdc.log"
echo ""
echo "Start service now? (y/n)"
read -r answer
if [ "$answer" = "y" ]; then
    service "$SERVICE_NAME" start
    service "$SERVICE_NAME" status
fi
