#!/bin/sh
set -eu


# TODO: change mac
MAC="4D:9E:51:18:AE:DE"

# wait for bluetoothd
for i in $(seq 1 20); do
  bluetoothctl show >/dev/null 2>&1 && break
  sleep 1
done

bluetoothctl power on >/dev/null 2>&1 || true
bluetoothctl trust "$MAC" >/dev/null 2>&1 || true
bluetoothctl connect "$MAC" >/dev/null 2>&1 || true

exit 0
