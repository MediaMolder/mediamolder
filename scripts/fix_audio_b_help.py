#!/usr/bin/env python3
"""Update extended help for audio encoder 'b' parameter in private_local/nodes.csv."""
import csv
import io
import os
import sys

REPO_ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
CSV_PATH = os.path.join(REPO_ROOT, "private_local", "nodes.csv")

AUDIO_B_EXTENDED = (
    "Sets the target average output bitrate in bits per second. "
    "For example, 128000 = 128 kbps, 320000 = 320 kbps. "
    "Higher values preserve more audio detail at the cost of a larger file; "
    "lower values save space at the cost of audible compression artefacts. "
    "For stereo content, 128-192 kbps is typical for AAC and Opus; "
    "192-320 kbps for MP3; 192-640 kbps for AC-3. "
    "This is also the target bitrate for CBR encodes where the encoder supports "
    "a constant bitrate mode."
)


def main():
    if not os.path.exists(CSV_PATH):
        sys.exit(f"error: {CSV_PATH} not found")

    rows = []
    updated = 0
    with open(CSV_PATH, newline="", encoding="utf-8") as f:
        reader = csv.DictReader(f)
        fieldnames = reader.fieldnames
        for row in reader:
            if (
                row["node_type"] == "encoder"
                and row.get("streams", "").strip() == "audio"
                and row["parameter"] == "b"
            ):
                row["extended help"] = AUDIO_B_EXTENDED
                updated += 1
            rows.append(row)

    out = io.StringIO()
    writer = csv.DictWriter(out, fieldnames=fieldnames)
    writer.writeheader()
    writer.writerows(rows)
    content = out.getvalue()

    with open(CSV_PATH, "w", newline="", encoding="utf-8") as f:
        f.write(content)

    print(f"Updated {updated} rows in {CSV_PATH}")


if __name__ == "__main__":
    main()
