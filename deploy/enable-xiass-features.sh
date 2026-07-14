#!/usr/bin/env bash
# XIASS API feature bootstrap: enable payment and login-agreement settings.

set -Eeuo pipefail

cd "$(dirname "$0")"

DB_CONTAINER="${DB_CONTAINER:-xiass-api-postgres}"
DB_USER="${DB_USER:-sub2api}"
DB_NAME="${DB_NAME:-sub2api}"

printf '正在向 XIASS API 数据库注入功能配置...\n'

docker exec -i "$DB_CONTAINER" psql -U "$DB_USER" -d "$DB_NAME" <<'EOF'
INSERT INTO settings (key, value) VALUES ('payment_enabled', 'true') ON CONFLICT (key) DO UPDATE SET value = 'true';
INSERT INTO settings (key, value) VALUES ('login_agreement_enabled', 'true') ON CONFLICT (key) DO UPDATE SET value = 'true';
INSERT INTO settings (key, value) VALUES ('login_agreement_mode', 'checkbox') ON CONFLICT (key) DO UPDATE SET value = 'checkbox';
INSERT INTO settings (key, value) VALUES (
  'login_agreement_documents',
  '[{"id":"terms","title":"用户服务协议","content_md":"欢迎使用 XIASS API。请遵守当地法律法规使用本服务。"},{"id":"privacy","title":"隐私政策","content_md":"我们重视您的隐私，您的使用数据和 API Key 仅用于计费和必要服务。我们不会滥用您的数据。"}]'
) ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value;
EOF

printf '配置注入成功，请刷新页面以查看效果。\n'
