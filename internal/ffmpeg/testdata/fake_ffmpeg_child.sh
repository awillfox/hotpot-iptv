#!/bin/sh
# Spawns a child that inherits stdout and outlives us. If only the parent is
# killed, the child keeps the write end of stdout open and a reader blocks.
sleep 60 &
sleep 60
