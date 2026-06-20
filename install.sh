#!/bin/bash

# API站 (Nowind-API) 交互式一键安装脚本
set -e

# Colors
GREEN="\033[32m"
YELLOW="\033[33m"
CYAN="\033[36m"
RED="\033[31m"
RESET="\033[0m"

echo -e "${CYAN}======================================================${RESET}"
echo -e "${CYAN}        欢迎使用 Nowind-API (API站) 交互式安装脚本    ${RESET}"
echo -e "${CYAN}======================================================${RESET}"
echo ""

# 交互配置
read -p "请输入安装路径 (默认: /opt/nowind-api/deploy): " DEPLOY_DIR
DEPLOY_DIR=${DEPLOY_DIR:-/opt/nowind-api/deploy}

read -p "请输入服务监听端口 (默认: 8080): " SERVER_PORT
SERVER_PORT=${SERVER_PORT:-8080}

read -p "请输入管理员邮箱 (默认: admin@example.com): " ADMIN_EMAIL
ADMIN_EMAIL=${ADMIN_EMAIL:-admin@example.com}

RANDOM_PASS=$(openssl rand -hex 8)
read -p "请输入管理员密码 (默认: 随机生成 ${RANDOM_PASS}): " ADMIN_PASSWORD
ADMIN_PASSWORD=${ADMIN_PASSWORD:-$RANDOM_PASS}

echo -e "\n${YELLOW}▶ 开始检测基础环境...${RESET}"

# 检查/安装 Docker
if ! command -v docker &> /dev/null; then
    echo -e "${YELLOW}未检测到 Docker，正在自动安装...${RESET}"
    curl -fsSL https://get.docker.com | bash -s docker
    systemctl enable --now docker
else
    echo -e "${GREEN}✓ Docker 已安装${RESET}"
fi

# 创建目录
echo -e "\n${YELLOW}▶ 创建部署目录: ${DEPLOY_DIR}${RESET}"
mkdir -p "$DEPLOY_DIR"
cd "$DEPLOY_DIR"

# 下载配置文件
echo -e "\n${YELLOW}▶ 下载配置文件...${RESET}"
curl -sSL "https://raw.githubusercontent.com/xyf0104/nowind-api/main/deploy/docker-compose.yml" -o docker-compose.yml
curl -sSL "https://raw.githubusercontent.com/xyf0104/nowind-api/main/deploy/.env.example" -o .env.example

echo -e "\n${YELLOW}▶ 生成配置信息...${RESET}"
cp .env.example .env

# 生成安全密钥
JWT_SECRET=$(openssl rand -hex 32)
TOTP_ENCRYPTION_KEY=$(openssl rand -hex 32)
POSTGRES_PASSWORD=$(openssl rand -hex 16)

# 替换配置
sed -i "s|^SERVER_PORT=.*|SERVER_PORT=${SERVER_PORT}|" .env
sed -i "s|^ADMIN_EMAIL=.*|ADMIN_EMAIL=${ADMIN_EMAIL}|" .env
sed -i "s|^ADMIN_PASSWORD=.*|ADMIN_PASSWORD=${ADMIN_PASSWORD}|" .env
sed -i "s|^JWT_SECRET=.*|JWT_SECRET=${JWT_SECRET}|" .env
sed -i "s|^TOTP_ENCRYPTION_KEY=.*|TOTP_ENCRYPTION_KEY=${TOTP_ENCRYPTION_KEY}|" .env
sed -i "s|^POSTGRES_PASSWORD=.*|POSTGRES_PASSWORD=${POSTGRES_PASSWORD}|" .env

echo -e "\n${YELLOW}▶ 开始拉取镜像并启动容器...${RESET}"
docker compose pull
docker compose up -d

# 获取机器 IP
SERVER_IP=$(curl -s ipv4.icanhazip.com || echo "你的服务器IP")

echo -e "\n${GREEN}======================================================${RESET}"
echo -e "${GREEN}🎉 恭喜！Nowind-API 部署完成并在后台运行！${RESET}"
echo -e "${GREEN}======================================================${RESET}"
echo -e "📂 部署路径:   ${CYAN}${DEPLOY_DIR}${RESET}"
echo -e "🌐 访问地址:   ${CYAN}http://${SERVER_IP}:${SERVER_PORT}${RESET}"
echo -e "👤 管理员账号: ${CYAN}${ADMIN_EMAIL}${RESET}"
echo -e "🔑 管理员密码: ${CYAN}${ADMIN_PASSWORD}${RESET}"
echo -e "${YELLOW}⚠️ 请妥善保管您的管理员账号密码，服务第一次启动可能需要 10-30 秒初始化。${RESET}"
echo -e "${CYAN}======================================================${RESET}\n"
