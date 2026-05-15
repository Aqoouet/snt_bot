#!/bin/bash
set -e
bash build.sh
ssh hostkey_us "systemctl stop snt-bot"
scp snt-bot hostkey_us:/opt/snt-bot/snt-bot
scp .env hostkey_us:/opt/snt-bot/.env
ssh hostkey_us "systemctl start snt-bot && sleep 2 && systemctl status snt-bot --no-pager"
