#!/usr/bin/env bash
# stack-guards.sh — repo conventions that `docker compose config` cannot check.
#
# In this repository the pinned tag in git IS the deployed version (Dockhand
# From-Git), so an unpinned image is not a style problem — it is a deployment
# that silently changes under you.
#
# Plain bash with grep only, so it runs on any workstation without Docker or
# Python: a rule enforced by something nobody can execute locally is not
# enforced at all.
#
# Usage: bash .github/scripts/stack-guards.sh   (from the repository root)
set -uo pipefail

FAILED=0
CHECKS=0
err()   { if [ -n "${GITHUB_ACTIONS:-}" ]; then echo "::error::$1"; else echo "  ✗ $1"; fi; FAILED=$((FAILED + 1)); }
ok()    { echo "  ✓ $1"; }
head_() { CHECKS=$((CHECKS + 1)); echo; echo "[$CHECKS] $1"; }

# --- 1. every image is pinned ----------------------------------------------
head_ "every image: is pinned to a tag or digest (never :latest, never bare)"
bad=0
while IFS= read -r line; do
  [ -z "$line" ] && continue
  file="${line%%:*}"; rest="${line#*:}"; lineno="${rest%%:*}"
  ref=$(echo "$line" | sed -E 's/.*image:[[:space:]]*//; s/[[:space:]]*$//; s/^["'"'"']//; s/["'"'"']$//')
  case "$ref" in
    *'${'*)      continue ;;                      # variable-driven, checked where it is defined
    *@sha256:*)  continue ;;                      # digest-pinned
    *:latest)    err "$file:$lineno pinned to :latest — '$ref'"; bad=1 ;;
    *:*)         continue ;;                      # tag-pinned
    *)           err "$file:$lineno has no tag or digest — '$ref'"; bad=1 ;;
  esac
done <<< "$(grep -rnE '^[[:space:]]*image:' --include='*.yml' --include='*.yaml' . 2>/dev/null | grep -v '/\.git/' || true)"
[ "$bad" = 0 ] && ok "all images pinned"

# --- 2. every stack ships a bilingual README --------------------------------
head_ "every stack has README.md and README.ru.md"
bad=0
while IFS= read -r compose; do
  [ -z "$compose" ] && continue
  d=$(dirname "$compose")
  [ -e "$d/README.md" ]    || { err "$d has no README.md"; bad=1; }
  [ -e "$d/README.ru.md" ] || { err "$d/README.md has no README.ru.md pair"; bad=1; }
done <<< "$(find . -name docker-compose.yml -not -path './.git/*' 2>/dev/null || true)"
[ "$bad" = 0 ] && ok "all stacks documented in both languages"

# --- 3. a decoy directory contains only web assets --------------------------
# Angie serves `root /var/www/html` with no location restrictions, and compose
# mounts ./www/${CAMO_SITE} there whole. So EVERY file in the chosen decoy's
# directory is fetchable by anyone probing the node — and a stray file is not
# merely untidy, it identifies the page as camouflage. Five upstream README.md
# files shipped this way: `curl https://<node>/README.md` returned wget
# instructions naming the template repository, which is a better fingerprint
# than an identical page would have been.
#
# Depth 2 and below is what gets mounted; a file directly in www/ never is, so
# repo-level notes (attribution, provenance) belong there or in the stack README.
head_ "remnanode/www/*: only web assets — anything else is served to probers"
bad=0
while IFS= read -r stray; do
  [ -z "$stray" ] && continue
  err "$stray sits in a served decoy directory and is not a web asset"
  bad=1
done <<< "$(find remnanode/www -mindepth 2 -type f 2>/dev/null \
            | grep -viE '\.(html|css|js|png|jpe?g|webp|svg|ico|webmanifest)$' || true)"
[ "$bad" = 0 ] && ok "decoy directories contain only web assets"

# --- verdict ----------------------------------------------------------------
echo
if [ "$FAILED" -gt 0 ]; then
  echo "FAILED: $FAILED violation(s) across $CHECKS guards"
  exit 1
fi
echo "OK: $CHECKS guards passed"
