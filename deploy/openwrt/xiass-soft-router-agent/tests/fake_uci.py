#!/usr/bin/env python3
import re
import sys
from pathlib import Path


def parse_args(argv):
    config_dir = Path("/etc/config")
    remaining = []
    idx = 0
    while idx < len(argv):
        value = argv[idx]
        if value == "-c":
            idx += 1
            config_dir = Path(argv[idx])
        elif value != "-q":
            remaining.append(value)
        idx += 1
    return config_dir, remaining


def read_option(path, option):
    if not path.exists():
        return None
    pattern = re.compile(r"^\s*option\s+%s\s+'(.*)'\s*$" % re.escape(option))
    for line in path.read_text(encoding="utf-8").splitlines():
        match = pattern.match(line)
        if match:
            return match.group(1).replace("'\\''", "'")
    return None


def set_option(path, option, value):
    path.parent.mkdir(parents=True, exist_ok=True)
    lines = path.read_text(encoding="utf-8").splitlines() if path.exists() else ["config agent 'main'"]
    pattern = re.compile(r"^\s*option\s+%s\s+" % re.escape(option))
    escaped = value.replace("'", "'\\''")
    replacement = "\toption %s '%s'" % (option, escaped)
    for idx, line in enumerate(lines):
        if pattern.match(line):
            lines[idx] = replacement
            break
    else:
        lines.append(replacement)
    path.write_text("\n".join(lines) + "\n", encoding="utf-8")


def main():
    config_dir, args = parse_args(sys.argv[1:])
    if not args:
        return 2
    command = args[0]
    if command == "commit":
        return 0
    if len(args) < 2:
        return 2
    expression = args[1]

    if command == "get":
        parts = expression.split(".")
        if len(parts) == 2 and parts[1] == "main":
            if (config_dir / parts[0]).exists():
                print("agent")
                return 0
            return 1
        if len(parts) != 3 or parts[1] != "main":
            return 1
        value = read_option(config_dir / parts[0], parts[2])
        if value is None:
            return 1
        print(value)
        return 0

    if command == "set":
        key, separator, value = expression.partition("=")
        if not separator:
            return 2
        parts = key.split(".")
        if len(parts) == 2 and parts[1] == "main":
            path = config_dir / parts[0]
            if not path.exists():
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_text("config %s 'main'\n" % (value or "agent"), encoding="utf-8")
            return 0
        if len(parts) != 3 or parts[1] != "main":
            return 2
        set_option(config_dir / parts[0], parts[2], value)
        return 0
    return 2


if __name__ == "__main__":
    raise SystemExit(main())
