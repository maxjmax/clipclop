#!/bin/bash
sel=$(echo "GET" | nc -N -U /tmp/clipclop.sock | dmenu -i -l 6)

[ -z "$sel" ] && exit 1

echo "SELECTING $sel"
echo "SEL $sel" | nc -N -U /tmp/clipclop.sock | xclip -selection clipboard
