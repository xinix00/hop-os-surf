#!/bin/sh
# Publishes the SURF apps as GitHub releases (assets, not binaries in some
# git history): first the full gate (host tests + tamago builds — only green
# publishes), then the elfs land twice:
#
#   rolling-release       rolling — stable URLs, always the latest green build
#   apps-YYYY.MM.DD-HHMM  pinned — for anyone who wants to stay put
#
# Any node and any boot config can pull them straight from a jobspec:
#
#   "artifacts": [{"url": "https://github.com/xinix00/hop-os-surf/releases/download/rolling-release/taskman.elf"}]
#
# Same artifact for every slot (canonically linked), same URL for everyone —
# no ad-hoc http servers next to your cluster (Derek 20-07).
set -e
cd "$(dirname "$0")/.."

APPS="display clock calc browser dash taskman launcher"
STAMP="$(date -u +%Y.%m.%d-%H%M)"
TAG="${TAG:-rolling-release}"
PIN="apps-$STAMP"

tools/test.sh

FILES=""
for app in $APPS; do
	FILES="$FILES out/$app.elf"
done

USAGE='Ready-to-run HopOS app images (arm64, canonically linked — one artifact runs in any slot). Use from a jobspec or boot config (`hopos.init[]`/`hopos.apps[]`):

```json
{"name":"taskman","driver":"hop",
 "artifacts":[{"url":"https://github.com/xinix00/hop-os-surf/releases/download/rolling-release/taskman.elf"}],
 "memory_limit":67108864,
 "env":{"SURF_ADDR":"{{host}}:7878","HOP_ADDR":"10.100.0.1:8080"}}
```'

# The pinned release: this exact build, forever.
# shellcheck disable=SC2086 — word splitting is intended
gh release create "$PIN" $FILES \
	--title "SURF apps $STAMP" \
	--notes "$USAGE

Pinned build, published $(date -u +%Y-%m-%dT%H:%MZ) by tools/publish-apps.sh (host tests + tamago gate green). For always-latest URLs use the \`rolling-release\` release."

# The rolling release: same assets, stable URLs.
if gh release view "$TAG" >/dev/null 2>&1; then
	# shellcheck disable=SC2086
	gh release upload "$TAG" $FILES --clobber
	gh release edit "$TAG" --notes "$USAGE

Rolling release: every publish replaces the assets, the URLs stay put. Currently $PIN — pin that tag if you want to stay on this exact build. Published by tools/publish-apps.sh (host tests + tamago gate green)."
else
	# shellcheck disable=SC2086
	gh release create "$TAG" $FILES --title "rolling-release" \
		--notes "$USAGE

Rolling release: every publish replaces the assets, the URLs stay put. Currently $PIN."
fi

echo "OK: rolling  https://github.com/xinix00/hop-os-surf/releases/download/rolling-release/<app>.elf" >&2
echo "OK: pinned   https://github.com/xinix00/hop-os-surf/releases/download/$PIN/<app>.elf" >&2
