#!/usr/bin/env bash
# NoWind API 推荐安装入口。仓库内执行时使用本地脚本，curl 管道执行时下载正式脚本。

set -Eeuo pipefail

if [ -f "./deploy/nowind-install.sh" ]; then
    exec bash ./deploy/nowind-install.sh "$@"
fi

exec bash <(curl -fsSL https://raw.githubusercontent.com/xyf0104/nowind-api/main/deploy/nowind-install.sh) "$@"
