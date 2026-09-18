#!/bin/bash
#
# cpdc-ccu start script for CentOS 5.x
# Usage: ./start.sh
# Feature: process keeps running after terminal exit
#

BASE_DIR=$(cd "$(dirname "$0")" && pwd)
BIN_FILE="$BASE_DIR/CPDC-CCU"
PID_FILE="$BASE_DIR/cpdc.pid"
LOG_FILE="$BASE_DIR/cpdc.log"

# check binary exists
if [ ! -f "$BIN_FILE" ]; then
    echo "Error: $BIN_FILE not found"
    exit 1
fi

# check if already running
if [ -f "$PID_FILE" ]; then
    PID=$(cat "$PID_FILE")
    if ps -p "$PID" > /dev/null 2>&1; then
        echo "cpdc-ccu is already running (pid: $PID)"
        exit 0
    fi
    rm -f "$PID_FILE"
fi

echo "Starting cpdc-ccu..."
# nohup: ignore SIGHUP so the process survives terminal exit
nohup "$BIN_FILE" >> "$LOG_FILE" 2>&1 &
PID=$!
echo $PID > "$PID_FILE"
echo "cpdc-ccu started (pid: $PID)"
echo "Log file: $LOG_FILE"