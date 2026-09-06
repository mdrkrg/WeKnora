#!/bin/sh

# Only emit whitelisted locale tags to avoid config.js injection from env values.
RUNTIME_DEFAULT_LOCALE=""
case "${DEFAULT_LOCALE:-}" in
  zh-CN|en-US|ru-RU|ko-KR|ja-JP) RUNTIME_DEFAULT_LOCALE="${DEFAULT_LOCALE}" ;;
esac

# 生成运行时配置文件，注入环境变量到前端
FILE_MB=${MAX_FILE_SIZE_MB:-50}
SKILL_MB=${MAX_SKILL_BUNDLE_SIZE_MB:-256}
if [ "$SKILL_MB" -lt "$FILE_MB" ] 2>/dev/null; then
  SKILL_MB=$FILE_MB
fi
if [ "$SKILL_MB" -gt 512 ] 2>/dev/null; then
  SKILL_MB=512
fi

cat > /usr/share/nginx/html/config.js << EOF
window.__RUNTIME_CONFIG__ = {
  MAX_FILE_SIZE_MB: ${FILE_MB},
  MAX_SKILL_BUNDLE_SIZE_MB: ${SKILL_MB},
  DEFAULT_LOCALE: "${RUNTIME_DEFAULT_LOCALE}"
};
EOF

# 处理 nginx 配置。
# 两个上限分开注入：全站保持知识库的 MAX_FILE_SIZE，只有技能 zip 上传的两条
# 集合路由放宽到 MAX_SKILL_BUNDLE_SIZE（不含 /install、PATCH 等子路径）。
# 合成一个全站上限会让每个上传端点都能收到技能包那么大的 body。
export MAX_FILE_SIZE=${FILE_MB}M
export MAX_SKILL_BUNDLE_SIZE=${SKILL_MB}M
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

envsubst '${MAX_FILE_SIZE} ${MAX_SKILL_BUNDLE_SIZE} ${APP_HOST} ${APP_PORT} ${APP_SCHEME} ${SPA_XFRAME_OPTIONS} ${SPA_FRAME_ANCESTORS}' \
  < /etc/nginx/templates/default.conf.template > /etc/nginx/conf.d/default.conf

# 启动 nginx
exec nginx -g 'daemon off;'
