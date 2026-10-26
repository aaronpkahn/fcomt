#!/usr/bin/env bash
# Install and (re)load the FComT launchd agent for the current user.
set -euo pipefail
REPO="$(cd "$(dirname "$0")/.." && pwd)"
LABEL=com.aaronkahn.fcomt
DEST="$HOME/Library/LaunchAgents/$LABEL.plist"

if [[ ! -x "$REPO/bin/fcomt-agent" ]]; then
  echo "missing $REPO/bin/fcomt-agent; run 'make build' first" >&2
  exit 1
fi
mkdir -p "$HOME/Library/LaunchAgents" "$REPO/logs"
cp "$REPO/deploy/$LABEL.plist" "$DEST"
launchctl bootout "gui/$(id -u)/$LABEL" 2>/dev/null || true
launchctl bootstrap "gui/$(id -u)" "$DEST"
launchctl list | grep fcomt || true
echo "installed $DEST; logs in $REPO/logs"
