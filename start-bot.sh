#!/bin/sh
nginx -g "daemon off;" &
exec moe-sticker-bot \
    --data_dir="${DATA_DIR:-/data}" \
    --log_level="${LOG_LEVEL:-info}" \
    --bot_token="${BOT_TOKEN}" \
    --db_addr="${DB_ADDR:-}" \
    --db_user="${DB_USER:-}" \
    --db_pass="${DB_PASS:-}" \
    --admin_uid="${ADMIN_UID:--1}" \
    --webapp_url="${WEBAPP_URL:-}" \
    --webapp_data_dir="${WEBAPP_DATA_DIR:-/data/webapp}" \
    --webhook_url="${WEBHOOK_URL:-}" \
    --webhook_secret="${WEBHOOK_SECRET:-}"
