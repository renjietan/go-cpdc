#!/bin/sh
#
# CPDC-IEC stop script
# Usage: /CPDC/bin/stop.sh
#

BASE_DIR="/CPDC/bin"
PID_FILE="$BASE_DIR/cpdc-iec.pid"

if [ ! -f "$PID_FILE" ]; then
    echo "cpdc-iec is not running"
    exit 0
fi

PID=$(cat "$PID_FILE")
if ! kill -0 "$PID" 2>/dev/null; then
    echo "cpdc-iec is not running"
    rm -f "$PID_FILE"
    exit 0
fi

echo "Stopping cpdc-iec (pid: $PID)..."
kill "$PID"

COUNT=0
while kill -0 "$PID" 2>/dev/null; do
    sleep 1
    COUNT=$((COUNT + 1))
    if [ "$COUNT" -ge 10 ]; then
        echo "Timeout, forcing kill..."
        kill -9 "$PID"
        break
    fi
done

rm -f "$PID_FILE"
echo "cpdc-iec stopped"
