#!/bin/sh
# Modes driven by first arg: ok | fail | stall
case "$1" in
ok)
  i=1
  while [ $i -le 3 ]; do
    echo "out_time_us=${i}000000"
    echo "progress=continue"
    i=$((i+1))
  done
  echo "progress=end"
  ;;
fail)
  echo "Conversion failed: broken input" >&2
  exit 1
  ;;
stall)
  echo "out_time_us=1000000"
  echo "progress=continue"
  sleep 30
  ;;
esac
