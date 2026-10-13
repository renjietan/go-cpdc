#!/bin/sh
#
# CPDC-IEC start script
# Usage: /CPDC/bin/start.sh
#

BASE_DIR="/CPDC/bin"
BIN_FILE="$BASE_DIR/CPDC-IEC"
PID_FILE="$BASE_DIR/cpdc-iec.pid"
LOG_FILE="$BASE_DIR/cpdc.log"

if [ ! -f "$BIN_FILE" ]; then
    echo "Error: $BIN_FILE not found" >&2
    exit 1
fi

if [ -f "$PID_FILE" ]; then
    PID=$(cat "$PID_FILE")
    if kill -0 "$PID" 2>/dev/null; then
        echo "cpdc-iec is already running (pid: $PID)"
        exit 0
    fi
    rm -f "$PID_FILE"
fi

echo "Starting cpdc-iec..."
nohup "$BIN_FILE" >> "$LOG_FILE" 2>&1 &
PID=$!
echo "$PID" > "$PID_FILE"
echo "cpdc-iec started (pid: $PID)"
echo "Log file: $LOG_FILE"
