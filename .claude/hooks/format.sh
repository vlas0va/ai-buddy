#!/usr/bin/env bash
# PostToolUse hook: format the file Claude just edited. Costs zero tokens; never blocks Claude.
file=$(python3 -c 'import json,sys; print(json.load(sys.stdin).get("tool_input",{}).get("file_path",""))' 2>/dev/null)
[ -z "$file" ] || [ ! -f "$file" ] && exit 0
case "$file" in
  *.go)            command -v gofmt >/dev/null && gofmt -w "$file" ;;
  *.py)            command -v ruff  >/dev/null && ruff format -q "$file" ;;
  *.ts|*.tsx|*.js|*.jsx|*.json|*.css)
                   [ -x "$CLAUDE_PROJECT_DIR/web/node_modules/.bin/prettier" ] && \
                   "$CLAUDE_PROJECT_DIR/web/node_modules/.bin/prettier" --write --log-level silent "$file" ;;
esac
exit 0
