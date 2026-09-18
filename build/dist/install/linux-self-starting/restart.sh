#!/bin/bash
#
# cpdc-ccu restart script for CentOS 5.x
# Usage: ./restart.sh
#

BASE_DIR=$(cd "$(dirname "$0")" && pwd)

"$BASE_DIR/stop.sh"
sleep 1
"$BASE_DIR/start.sh"