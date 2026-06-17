#!/bin/bash
# =============================================================================
# NoWind API - 自动开启定制功能脚本
# 作用：连接 Docker 容器中的 PostgreSQL，注入支付功能和合规文档配置
# =============================================================================

# 确保在 deploy 目录执行
cd "$(dirname "$0")"

DB_CONTAINER="sub2api-db"
DB_USER="sub2api"
DB_NAME="sub2api"

echo "正在向数据库注入功能配置..."

docker exec -i $DB_CONTAINER psql -U $DB_USER -d $DB_NAME << 'EOF'
-- 1. 开启支付和充值功能
INSERT INTO settings (key, value) VALUES ('payment_enabled', 'true') ON CONFLICT (key) DO UPDATE SET value = 'true';

-- 2. 开启登录协议合规功能
INSERT INTO settings (key, value) VALUES ('login_agreement_enabled', 'true') ON CONFLICT (key) DO UPDATE SET value = 'true';
INSERT INTO settings (key, value) VALUES ('login_agreement_mode', 'checkbox') ON CONFLICT (key) DO UPDATE SET value = 'checkbox';

-- 3. 注入默认条款文档 (用户服务协议 & 隐私政策)
INSERT INTO settings (key, value) VALUES (
  'login_agreement_documents',
  '[{"id":"terms","title":"用户服务协议","content_md":"欢迎使用 NoWind API。请遵守当地法律法规使用本服务。"},{"id":"privacy","title":"隐私政策","content_md":"我们重视您的隐私，您的使用数据和 API Key 仅用于计费和必要服务。我们不会滥用您的数据。"}]'
) ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value;

EOF

echo "配置注入成功！请刷新页面以查看效果。"
