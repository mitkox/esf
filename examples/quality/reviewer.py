#!/usr/bin/python3
"""Deterministic integration fixture. This is not a production reviewer."""
import json
import re
import sys

if "--version" in sys.argv:
    print("fixture-reviewer-v1")
    sys.exit(0)

with open("/workspace/.factory/review-prompt.txt", encoding="utf-8") as report_prompt:
    digests = re.findall(r"sha256:[a-f0-9]{64}", report_prompt.read())
if len(digests) < 2:
    sys.exit("Missing candidate and plan bindings")
print(json.dumps({
    "candidate_digest": digests[0],
    "plan_digest": digests[1],
    "verdict": "approve",
    "findings": [],
}))
