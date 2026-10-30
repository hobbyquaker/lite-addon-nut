#!/bin/sh
#
#   Builds the addon packages: dist/nut-<arch>-<version>.tar.gz and .sha256 for aarch64, x86_64
#   and armv7l (the asset names openccu-lite's catalogue resolves by uname -m).
#
#     VERSION=0.1.0 scripts/build.sh            # all three
#     ARCHES=x86_64 scripts/build.sh            # one
#     SKIP_UI=1 scripts/build.sh                # keep addon/nut/app as it is
#
#   The page is built with npm in ui/ and copied to addon/nut/app (the daemon serves it at
#   /addons/nut/); the manifest's icons, the 503 page and the proxy rule live in addon/nut/www
#   and addon/nut/etc, tracked, where the catalogue reads the icons from the repository at the
#   release tag; the daemon is a static Go binary (CGO_ENABLED=0) per architecture.
#
set -e
cd "$(dirname "$0")/.."
VERSION=${VERSION:-0.0.0-dev}
ARCHES=${ARCHES:-aarch64 x86_64 armv7l}
GO=${GO:-go}

if [ -z "$SKIP_UI" ]; then
    (cd ui && npm ci --no-audit --no-fund && npm run build)
    rm -rf addon/nut/app
    mkdir -p addon/nut/app
    cp -a ui/dist/. addon/nut/app/
fi
cp -a addon/static/. addon/nut/www/
printf 'VERSION_ADDON=%s\n' "$VERSION" > addon/nut/versions

mkdir -p dist
for arch in $ARCHES; do
    case $arch in
        aarch64) goarch=arm64; goarm= ;;
        x86_64) goarch=amd64; goarm= ;;
        armv7l) goarch=arm; goarm=7 ;;
        *) echo "unknown architecture $arch" >&2; exit 1 ;;
    esac
    mkdir -p addon/nut/bin
    rm -f addon/nut/bin/nut
    CGO_ENABLED=0 GOOS=linux GOARCH=$goarch GOARM=$goarm $GO build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o addon/nut/bin/nut ./cmd/nut

    out=dist/nut-$arch-$VERSION.tar.gz
    stage=$(mktemp -d)
    cp -a addon/update_script addon/openccu-lite.json addon/nut "$stage/"
    rm -rf "$stage/nut/var"
    find "$stage" \( -name '._*' -o -name '.DS_Store' \) -exec rm -f {} +
    chmod 755 "$stage/update_script" "$stage/nut/rc.d" "$stage/nut/bin/nut"
    test -f "$stage/nut/etc/lighttpd.conf"
    COPYFILE_DISABLE=1 tar --owner=root --group=root --sort=name --mtime='2026-01-01 00:00Z' -czf "$out" -C "$stage" .
    rm -rf "$stage"
    (cd dist && sha256sum "$(basename "$out")" > "$(basename "$out").sha256")
    echo "built $out"
done
