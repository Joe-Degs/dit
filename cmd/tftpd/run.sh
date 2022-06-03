#!/bin/sh

./build.sh
tftpd -h 2>&1 | less
