#!/bin/bash
#
# cpdc-ccu stop script for CentOS 5.x
# Usage: ./stop.sh
#

BASE_DIR=$(cd "$(dirname "$0")" && pwd)
PID_FILE="$BASE_DIR/cpdc.pid"
BIN_NAME="CPDC-CCU"

# try to find PID from pid file or process list
if [ ! -f "$PID_FILE" ]; then
    PID=$(ps -ef | grep "$BIN_NAME" | grep -v grep | awk '{print $2}')
    if [ -z "$PID" ]; then
        echo "cpdc-ccu is not running"
        exit 0
    fi
    echo "Found cpdc-ccu (pid: $PID), stopping..."
else
    PID=$(cat "$PID_FILE")
    if ! ps -p "$PID" > /dev/null 2>&1; then
        echo "cpdc-ccu is not running"
        rm -f "$PID_FILE"
        exit 0
    fi
    echo "Stopping cpdc-ccu (pid: $PID)..."
fi

# graceful shutdown
kill "$PID"

# wait up to 10 seconds
COUNT=0
while ps -p "$PID" > /dev/null 2>&1; do
    sleep 1
    COUNT=$((COUNT + 1))
    if [ $COUNT -ge 10 ]; then
        echo "Timeout, forcing kill..."
        kill -9 "$PID"
        break
    fi
done

rm -f "$PID_FILE"
echo "cpdc-ccu stopped"