#!/usr/bin/env bash
# validate_examples.sh - Run mediamolder validate on all example and community-script configs
# in two passes: static (--no-probe) and probe (real input files substituted).
set -uo pipefail

BIN=${BIN:-./mediamolder}
INPUT=${INPUT:-testdata/BBB_10sec.mp4}
INPUT2=${INPUT2:-testdata/BBB_10sec.mp4}
AUDIO=${AUDIO:-testdata/sample.aac}
IMAGE=${IMAGE:-testdata/sample.jpg}
RAW_YUV=${RAW_YUV:-testdata/sample.yuv}
OUTPUT=${OUTPUT:-/dev/null}
PASSLOG=${PASSLOG:-/tmp/passlog}
LOUDNORM_STATS=${LOUDNORM_STATS:-/tmp/loudnorm.json}

pass_static=0
warn_static=0
err_static=0
pass_probe=0
warn_probe=0
err_probe=0

TMPWORK=$(mktemp -d)
trap 'rm -rf "$TMPWORK"' EXIT

static_errors=""
probe_errors=""
probe_warnings=""

for json in testdata/examples/*.json testdata/community-scripts/*.json; do
  name=$(basename "$json")

  # ------ PASS 1: static (--no-probe) ------
  out=$("$BIN" validate --no-probe "$json" 2>&1)
  ec=$?
  if [ $ec -eq 0 ]; then
    pass_static=$((pass_static + 1))
  elif [ $ec -eq 2 ]; then
    warn_static=$((warn_static + 1))
  else
    err_static=$((err_static + 1))
    static_errors="$static_errors\n[STATIC ERR] $name"
    msg=$(printf '%s' "$out" | grep -v '^NOTICE' | head -3)
    static_errors="$static_errors\n  $msg"
  fi

  # ------ PASS 2: probe with real inputs ------
  tmp="$TMPWORK/$name"
  sed \
    -e "s|{{input}}|$INPUT|g" \
    -e "s|{{input2}}|$INPUT2|g" \
    -e "s|{{audio}}|$AUDIO|g" \
    -e "s|{{image}}|$IMAGE|g" \
    -e "s|{{raw_yuv}}|$RAW_YUV|g" \
    -e "s|{{output}}|$OUTPUT|g" \
    -e "s|{{passlog}}|$PASSLOG|g" \
    -e "s|{{loudnorm_stats}}|$LOUDNORM_STATS|g" \
    "$json" > "$tmp"

  out=$("$BIN" validate "$tmp" 2>&1)
  ec=$?
  if [ $ec -eq 0 ]; then
    pass_probe=$((pass_probe + 1))
  elif [ $ec -eq 2 ]; then
    warn_probe=$((warn_probe + 1))
    probe_warnings="$probe_warnings\n  $name"
    issues=$(printf '%s' "$out" | grep -v '^NOTICE' | grep -v '^summary' | head -4)
    probe_warnings="$probe_warnings\n    $issues"
  else
    err_probe=$((err_probe + 1))
    probe_errors="$probe_errors\n[PROBE ERR] $name"
    msg=$(printf '%s' "$out" | grep -v '^NOTICE' | head -5)
    probe_errors="$probe_errors\n  $msg"
  fi
done

printf "\n=== STATIC (--no-probe) ===\n"
printf "  OK: %d  WARN: %d  ERR: %d\n" "$pass_static" "$warn_static" "$err_static"
if [ -n "$static_errors" ]; then
  printf "%b\n" "$static_errors"
fi

printf "\n=== PROBE (real inputs) ===\n"
printf "  OK: %d  WARN: %d  ERR: %d\n" "$pass_probe" "$warn_probe" "$err_probe"
if [ -n "$probe_errors" ]; then
  printf "%b\n" "$probe_errors"
fi
if [ -n "$probe_warnings" ]; then
  printf "  Warnings detail:%b\n" "$probe_warnings"
fi

# Exit non-zero if any errors found
if [ "$err_static" -gt 0 ] || [ "$err_probe" -gt 0 ]; then
  exit 1
fi
