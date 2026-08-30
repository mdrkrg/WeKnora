#!/bin/sh

# 生成运行时配置文件，注入环境变量到前端
cat > /usr/share/nginx/html/config.js << EOF
window.__RUNTIME_CONFIG__ = {
  MAX_FILE_SIZE_MB: ${MAX_FILE_SIZE_MB:-50}
};
EOF

# 处理 nginx 配置
export MAX_FILE_SIZE=${MAX_FILE_SIZE_MB}M
export APP_HOST=${APP_HOST:-app}
export APP_PORT=${APP_PORT:-8080}
export APP_SCHEME=${APP_SCHEME:-http}

# LTI 自托管 handoff：开启时 SPA 根（/）需可被平台 iframe 嵌套，handoff 成功
# 后 302 落在 /#lti_result，整站跑在平台 iframe 里。此时以 CSP frame-ancestors
# （默认 'self'，或 LTI_FRAME_ANCESTORS 配置的平台来源）取代 X-Frame-Options；
# 默认维持 SAMEORIGIN 且 CSP 头为空（不生效），行为与旧版完全一致。
SPA_XFRAME_OPTIONS="SAMEORIGIN"
SPA_FRAME_ANCESTORS=""
if [ "${LTI_ENABLE:-false}" = "true" ] && [ "${LTI_SELF_HANDOFF_ENABLE:-false}" = "true" ]; then
    SPA_XFRAME_OPTIONS=""
    SPA_FRAME_ANCESTORS="frame-ancestors ${LTI_FRAME_ANCESTORS:-'self'}"
fi
export SPA_XFRAME_OPTIONS SPA_FRAME_ANCESTORS

envsubst '${MAX_FILE_SIZE} ${APP_HOST} ${APP_PORT} ${APP_SCHEME} ${SPA_XFRAME_OPTIONS} ${SPA_FRAME_ANCESTORS}' < /etc/nginx/templates/default.conf.template > /etc/nginx/conf.d/default.conf

# 启动 nginx
exec nginx -g 'daemon off;'
