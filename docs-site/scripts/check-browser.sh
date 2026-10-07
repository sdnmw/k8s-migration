#!/bin/sh
# Run against the deployed Web/Nginx URL, not `docusaurus serve` (which can
# redirect JSON index requests when trailingSlash is enabled).
set -eu
docs_origin=${1:-http://127.0.0.1:4179}
docs_cli=${PLAYWRIGHT_CLI:-/Users/mw/.codex/skills/playwright/scripts/playwright_cli.sh}
"$docs_cli" -s=docs-browser-check open "$docs_origin/docs/"
script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
"$docs_cli" -s=docs-browser-check run-code --filename="$script_dir/check-browser.cjs"
