#!/bin/bash
set -e
BUILD_TIME=$(date '+%d.%m.%Y %H:%M')
GOOS=linux GOARCH=amd64 go build -ldflags "-X 'main.buildTime=${BUILD_TIME}'" -o snt-bot .
echo "built: ${BUILD_TIME}"
