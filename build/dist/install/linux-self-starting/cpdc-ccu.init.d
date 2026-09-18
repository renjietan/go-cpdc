#!/bin/bash
#
# cpdc-ccu - CPDC-CCU 服务启动脚本（SysVinit）
#
# chkconfig: 2345 90 10
# description: CPDC-CCU 数据集中控制服务
# processname: CPDC-CCU
#

PROG="CPDC-CCU"
PROG_PATH="/home/TEDS/CPDC"
PROG_FILE="$PROG_PATH/$PROG"
PID_FILE="$PROG_PATH/cpdc.pid"
LOCK_FILE="/var/lock/subsys/cpdc-ccu"

start() {
    if [ -f "$PID_FILE" ]; then
        PID=$(cat "$PID_FILE")
        if ps -p "$PID" > /dev/null 2>&1; then
            echo "$PROG is already running (pid: $PID)"
            return 1
        fi
        rm -f "$PID_FILE"
    fi

    if [ ! -f "$PROG_FILE" ]; then
        echo "Error: $PROG_FILE not found"
        return 1
    fi

    echo "Starting $PROG..."
    cd "$PROG_PATH" || return 1
    sleep 5
    /bin/bash "$PROG_PATH/start.sh" >> "$PROG_PATH/cpdc-service.log" 2>&1
    RET=$?
    if [ $RET -ne 0 ]; then
        echo "Failed to start $PROG via start.sh"
        return 1
    fi
    touch "$LOCK_FILE"
    echo "$PROG started"
}

stop() {
    cd "$PROG_PATH" || return 1
    echo "Stopping $PROG..."
    /bin/bash "$PROG_PATH/stop.sh"
    RET=$?
    rm -f "$LOCK_FILE"
    if [ $RET -ne 0 ]; then
        echo "Failed to stop $PROG"
        return 1
    fi
    echo "$PROG stopped"
}

status() {
    if [ -f "$PID_FILE" ]; then
        PID=$(cat "$PID_FILE")
        if ps -p "$PID" > /dev/null 2>&1; then
            echo "$PROG is running (pid: $PID)"
            return 0
        fi
    fi

    PID=$(ps -ef | grep "$PROG" | grep -v grep | awk '{print $2}')
    if [ -n "$PID" ]; then
        echo "$PROG is running (pid: $PID)"
        return 0
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
