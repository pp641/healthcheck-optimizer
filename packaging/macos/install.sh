#!/bin/sh
# Installs the tidyfleet binary and its per-user LaunchAgent.
#   ./install.sh [path/to/tidyfleet]     install or upgrade
#   ./install.sh --uninstall             stop the agent and remove it (keeps settings)
set -eu

LABEL=com.tidyfleet.agent
HERE=$(cd "$(dirname "$0")" && pwd)
BIN_DIR="$HOME/.local/bin"
BIN="$BIN_DIR/tidyfleet"
PLIST="$HOME/Library/LaunchAgents/$LABEL.plist"
LOG="$HOME/Library/Logs/Tidyfleet/agent.log"
DOMAIN="gui/$(id -u)"

if [ "${1:-}" = "--uninstall" ]; then
  launchctl bootout "$DOMAIN/$LABEL" 2>/dev/null || true
  rm -f "$PLIST" "$BIN"
  echo "Removed the Tidyfleet agent. Settings remain in ~/Library/Application Support/Tidyfleet."
  exit 0
fi

SRC=${1:-"$HERE/../../bin/tidyfleet-darwin-$(uname -m | sed 's/x86_64/amd64/')"}
[ -f "$SRC" ] || { echo "binary not found: $SRC (run 'make agent-darwin' first)" >&2; exit 1; }

mkdir -p "$BIN_DIR" "$(dirname "$PLIST")" "$(dirname "$LOG")"
install -m 0755 "$SRC" "$BIN"
sed -e "s|__BIN__|$BIN|g" -e "s|__LOG__|$LOG|g" "$HERE/$LABEL.plist" > "$PLIST"
plutil -lint "$PLIST" >/dev/null

launchctl bootout "$DOMAIN/$LABEL" 2>/dev/null || true
launchctl bootstrap "$DOMAIN" "$PLIST"
echo "Installed $BIN and started the background agent (log: $LOG)."
case ":$PATH:" in *":$BIN_DIR:"*) ;; *) echo "Add $BIN_DIR to your PATH to use the tidyfleet command." ;; esac
