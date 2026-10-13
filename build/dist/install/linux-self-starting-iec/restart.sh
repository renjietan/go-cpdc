#!/bin/sh
#
# CPDC-IEC restart script
# Usage: /CPDC/bin/restart.sh
#

BASE_DIR="/CPDC/bin"

"$BASE_DIR/stop.sh"
sleep 1
"$BASE_DIR/start.sh"
