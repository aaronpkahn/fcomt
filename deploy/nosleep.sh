#!/usr/bin/env bash
# Keep the Mac awake 24/7 so monitoring never pauses (needs sudo).
# Display sleep is allowed; system and disk sleep are disabled.
set -euo pipefail
sudo pmset -a sleep 0
sudo pmset -a disksleep 0
sudo pmset -a standby 0 2>/dev/null || true
sudo pmset -a autopoweroff 0 2>/dev/null || true
sudo pmset -a powernap 0 2>/dev/null || true
sudo pmset -a womp 1            # wake on network access
sudo pmset -a autorestart 1     # restart after power failure
pmset -g
