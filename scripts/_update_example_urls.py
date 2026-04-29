"""Stamp {{input}} / {{output}} placeholders into testdata/examples/*.json.

Run from the repo root:
    python3 scripts/_update_example_urls.py
"""
import re
import os
import glob

EXAMPLES_DIR = "testdata/examples"

# Files whose output IDs need a per-ID variable name instead of plain {{output}}.
SPECIAL = {
    "35_abr_ladder.json": [
        "{{input}}",
        "{{out_1080}}",
        "{{out_720}}",
        "{{out_540}}",
        "{{out_360}}",
    ],
}

URL_PATTERN = re.compile(r'"url"\s*:\s*"[^"]*"')

for filepath in sorted(glob.glob(os.path.join(EXAMPLES_DIR, "*.json"))):
    basename = os.path.basename(filepath)
    with open(filepath) as fh:
        content = fh.read()

    matches = list(URL_PATTERN.finditer(content))
    if not matches:
        print(f"SKIP (no url fields): {basename}")
        continue

    if basename in SPECIAL:
        replacements = SPECIAL[basename]
    else:
        # First occurrence is always the input, the rest are outputs.
        replacements = ["{{input}}"] + ["{{output}}"] * (len(matches) - 1)

    if len(matches) != len(replacements):
        print(f"WARN mismatch ({len(matches)} url fields vs "
              f"{len(replacements)} replacements): {basename}")
        continue

    # Replace from the end so earlier offsets stay valid.
    result = content
    for match, rep in zip(reversed(matches), reversed(replacements)):
        result = result[: match.start()] + f'"url": "{rep}"' + result[match.end() :]

    if result != content:
        with open(filepath, "w") as fh:
            fh.write(result)
        print(f"OK: {basename}")
    else:
        print(f"UNCHANGED: {basename}")
