#!/bin/sh
#
# cpdc-iec - CPDC-IEC service (SysVinit fallback)
#
# chkconfig: 2345 90 10
# description: CPDC-IEC service
# processname: CPDC-IEC
#

PROG="CPDC-IEC"
PROG_PATH="/CPDC/bin"
PID_FILE="$PROG_PATH/cpdc-iec.pid"

start() {
    if [ -f "$PID_FILE" ]; then
        PID=$(cat "$PID_FILE")
        if kill -0 "$PID" 2>/dev/null; then
            echo "$PROG is already running (pid: $PID)"
            return 0
        fi
        rm -f "$PID_FILE"
    fi

    echo "Starting $PROG..."
    "$PROG_PATH/start.sh"
    return $?
}

stop() {
    echo "Stopping $PROG..."
    "$PROG_PATH/stop.sh"
    return $?
}

status() {
    if [ -f "$PID_FILE" ]; then
        PID=$(cat "$PID_FILE")
        if kill -0 "$PID" 2>/dev/null; then
            echo "$PROG is running (pid: $PID)"
            return 0
        fi
    fi
    echo "$PROG is not running"
    return 1
}

restart() {
    stop
    sleep 1
    start
}

case "$1" in
    start)
        start
        ;;
    stop)
        stop
        ;;
    status)
        status
        ;;
    restart)
        restart
        ;;
    *)
        echo "Usage: $0 {start|stop|status|restart}"
        exit 1
        ;;
esac
