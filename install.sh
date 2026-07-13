#!/usr/bin/env bash
# XIASS API 推荐安装入口。仓库内执行时使用本地脚本，curl 管道执行时下载正式脚本。

set -Eeuo pipefail

if [ -f "./deploy/xiass-install.sh" ]; then
    exec bash ./deploy/xiass-install.sh "$@"
fi

exec bash <(curl -fsSL https://raw.githubusercontent.com/xyf0104/xiass-api/main/deploy/xiass-install.sh) "$@"
