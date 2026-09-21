#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CONFIG="$ROOT/pm2.config.js"
APP="tdx-research"
ENV_FILE="${TDX_RESEARCH_ENV_FILE:-/etc/tdx-research.env}"
BASE_URL="${TDX_RESEARCH_BASE_URL:-http://127.0.0.1:8081}"

load_env() {
  if [[ ! -r "$ENV_FILE" ]]; then
    echo "无法读取环境文件: $ENV_FILE" >&2
    echo "请复制 deploy/tdx-research.env.example 并设置 TDX_API_TOKEN、TDX_VIPDOC_DIR。" >&2
    exit 1
  fi
  set -a
  # shellcheck disable=SC1090
  source "$ENV_FILE"
  set +a
  : "${TDX_API_TOKEN:?TDX_API_TOKEN 未配置}"
  : "${TDX_VIPDOC_DIR:?TDX_VIPDOC_DIR 未配置}"
  if [[ "$TDX_VIPDOC_DIR" != /* ]]; then
    echo "TDX_VIPDOC_DIR 必须是 Debian 绝对路径: $TDX_VIPDOC_DIR" >&2
    exit 1
  fi
}

check_runtime() {
  command -v pm2 >/dev/null 2>&1 || { echo "未安装 pm2" >&2; exit 1; }
  [[ -x "$ROOT/output/bin/tdx-research" ]] || {
    echo "缺少可执行文件: $ROOT/output/bin/tdx-research" >&2
    echo "先执行: CGO_ENABLED=1 go build -trimpath -o output/bin/tdx-research ./cmd/tdx-research" >&2
    exit 1
  }
  mkdir -p "$ROOT/output/logs" "$ROOT/output/research"
}

check_downloader() {
  [[ -x "$ROOT/output/bin/tdx-down" ]] || {
    echo "缺少下载工具: $ROOT/output/bin/tdx-down" >&2
    echo "先执行: go build -trimpath -o output/bin/tdx-down ./cmd/tdx-down" >&2
    exit 1
  }
}

api_job() {
  local kind="$1"
  load_env
  curl --fail --silent --show-error \
    -X POST \
    -H "Authorization: Bearer $TDX_API_TOKEN" \
    -H "Content-Type: application/json" \
    -d '{"symbols":[]}' \
    "$BASE_URL/v1/jobs/$kind"
  echo
}

case "${1:-help}" in
  start)
    load_env
    check_runtime
    pm2 start "$CONFIG" --only "$APP" --update-env
    pm2 save
    ;;
  restart)
    load_env
    check_runtime
    pm2 restart "$APP" --update-env
    pm2 save
    ;;
  reload)
    load_env
    check_runtime
    pm2 reload "$APP" --update-env
    ;;
  stop) pm2 stop "$APP" ;;
  delete) pm2 delete "$APP"; pm2 save ;;
  status) pm2 describe "$APP" ;;
  logs) pm2 logs "$APP" --lines "${2:-100}" ;;
  down)
    load_env
    check_downloader
    "$ROOT/output/bin/tdx-down" -download-dir "$ROOT/output/hsjday"
    ;;
  import) api_job import ;;
  update) api_job update ;;
  daily) api_job daily ;;
  jobs)
    load_env
    curl --fail --silent --show-error \
      -H "Authorization: Bearer $TDX_API_TOKEN" "$BASE_URL/v1/jobs"
    echo
    ;;
  help|-h|--help)
    echo "用法: $0 {start|restart|reload|stop|delete|status|logs [行数]|down|import|update|daily|jobs}"
    echo "环境文件: TDX_RESEARCH_ENV_FILE（默认 /etc/tdx-research.env）"
    ;;
  *) echo "未知命令: $1" >&2; exit 2 ;;
esac
