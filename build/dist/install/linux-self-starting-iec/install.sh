#!/bin/sh
#
# CPDC-IEC auto-start installer (systemd, recommended for ARM64 Linux)
# Usage: sudo /CPDC/bin/install.sh
#

set -e

SCRIPT_DIR="/CPDC/bin"
SERVICE_NAME="cpdc-iec"
UNIT_SRC="$SCRIPT_DIR/cpdc-iec.service"
UNIT_DST="/etc/systemd/system/$SERVICE_NAME.service"

if [ "$(id -u)" -ne 0 ]; then
    echo "Error: Please run as root (sudo)" >&2
    exit 1
fi

if ! command -v systemctl >/dev/null 2>&1; then
    echo "Error: systemctl not found. Use the init.d fallback instead." >&2
    exit 1
fi

if [ ! -f "$UNIT_SRC" ]; then
    echo "Error: $UNIT_SRC not found" >&2
    exit 1
fi

echo "Installing systemd service..."
cp "$UNIT_SRC" "$UNIT_DST"
chmod 644 "$UNIT_DST"

systemctl daemon-reload
systemctl enable "$SERVICE_NAME.service"

echo ""
echo "=== Installation Complete ==="
echo "Start:     systemctl start $SERVICE_NAME"
echo "Stop:      systemctl stop $SERVICE_NAME"
echo "Restart:   systemctl restart $SERVICE_NAME"
echo "Status:    systemctl status $SERVICE_NAME"
echo "Auto-start: enabled"
echo ""
