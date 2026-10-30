#!/bin/sh
#
#   Runs the daemon against the fake system and the fake upsd, for the page's development and the
#   Playwright tests: the fake system on 127.0.0.1:18080 (its upsd on 127.0.0.1:13493), the page on
#   http://127.0.0.1:18297/addons/nut/ (no session check). A fresh configuration directory each
#   run unless NUT_CONF_DIR names one. The page is served from ui/dist (npm run build first).
#
set -e
cd "$(dirname "$0")/.."
DIR=${NUT_CONF_DIR:-$(mktemp -d)}
mkdir -p build "$DIR/addon"
go build -o build/fakesystem ./cmd/fakesystem
go build -o build/nut-dev ./cmd/nut
echo dev > "$DIR/token"
build/fakesystem -addr 127.0.0.1:18080 -upsd 127.0.0.1:13493 &
FAKE=$!
sleep 0.5
build/nut-dev -system http://127.0.0.1:18080 -no-session-check -port 18297 -conf-dir "$DIR" -addon-dir "$DIR/addon" -www ui/dist -token-file "$DIR/token" &
DAEMON=$!
trap 'kill $DAEMON $FAKE 2>/dev/null' EXIT INT TERM
wait $DAEMON
