#!/usr/bin/env python3
"""Takes the files the container shares with the VM out of cloud-init.yaml, so the two cannot drift apart:
the hardening of sshd, the tmux configuration and the settings of VS Code Remote-SSH.

usage: seed.py cloud-init.yaml ROOT
Files under /home/builder/ go to ROOT/home/builder/..., /etc/ssh/... to ROOT/etc/ssh/...
"""
import os
import sys

import yaml

WANTED = {
    "/etc/ssh/sshd_config.d/01-keys-only.conf",
    "/home/builder/.tmux.conf",
    "/home/builder/.vscode-server/data/Machine/settings.json",
}


def main(src, root):
    with open(src, encoding="utf-8") as f:
        doc = yaml.safe_load(f)
    found = set()
    for w in doc.get("write_files", []):
        path = w["path"]
        if path not in WANTED:
            continue
        target = os.path.join(root, path.lstrip("/"))
        os.makedirs(os.path.dirname(target), exist_ok=True)
        with open(target, "w", encoding="utf-8") as out:
            out.write(w["content"])
        os.chmod(target, int(w.get("permissions", "0644"), 8))
        found.add(path)
    missing = WANTED - found
    if missing:
        sys.exit("seed.py: not in cloud-init.yaml: " + ", ".join(sorted(missing)))


if __name__ == "__main__":
    if len(sys.argv) != 3:
        sys.exit(__doc__)
    main(sys.argv[1], sys.argv[2])
