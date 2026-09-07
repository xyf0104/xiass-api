#!/bin/sh
set -eu

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$repo_root"

# Compose config only renders configuration; it does not contact a Docker daemon.
python3 - <<'PY'
import json
import os
from pathlib import Path
import subprocess

examples = dict(
    line.split("=", 1)
    for line in Path("deploy/.env.example").read_text().splitlines()
    if line.startswith("LOG_") and "=" in line
)
assert examples["LOG_ROTATION_MAX_SIZE_MB"] == "10"
assert examples["LOG_ROTATION_MAX_BACKUPS"] == "3"
assert examples["LOG_ROTATION_MAX_AGE_DAYS"] == "7"
assert examples["LOG_ROTATION_COMPRESS"] == "true"
assert examples["LOG_OUTPUT_TO_FILE"] == "true"

env = {key: value for key, value in os.environ.items()
       if not key.startswith(("LOG_", "COMPOSE_"))}
env.update(POSTGRES_PASSWORD="logging-test-placeholder", JWT_SECRET="x" * 32,
           DATABASE_HOST="postgres.example.invalid", DATABASE_PASSWORD="logging-test-placeholder",
           REDIS_HOST="redis.example.invalid")
overrides = dict(examples)
overrides.update(LOG_ROTATION_MAX_SIZE_MB="25", LOG_ROTATION_MAX_BACKUPS="0",
                 LOG_ROTATION_MAX_AGE_DAYS="0", LOG_ROTATION_COMPRESS="false",
                 LOG_ROTATION_LOCAL_TIME="false", LOG_OUTPUT_FILE_PATH="/app/data/logs/custom.log")

for filename in ("docker-compose.yml", "docker-compose.local.yml",
                 "docker-compose.standalone.yml", "docker-compose.dev.yml"):
    for name, values in (("unset", {}), ("example", examples), ("override", overrides)):
        result = subprocess.run(
            ["docker", "compose", "--env-file", "/dev/null", "--profile", "*", "-f", "deploy/" + filename,
             "config", "--format", "json"],
            env={**env, **values}, text=True, capture_output=True, check=True,
        )
        services = json.loads(result.stdout)["services"]
        expected = {"xiass-api", "team-child-browser", "team-child-automation"}
        if filename != "docker-compose.dev.yml":
            expected.add("watchtower")
        if filename != "docker-compose.standalone.yml":
            expected.update(("postgres", "redis"))
        assert set(services) == expected, (filename, name, set(services))
        for service, config in services.items():
            assert config["logging"] == {
                "driver": "json-file", "options": {"max-size": "10m", "max-file": "3"}
            }, (filename, name, service)
        actual = services["xiass-api"]["environment"]
        for key in examples:
            assert actual[key] == values.get(key, ""), (filename, name, key)
    print(filename + ": all service caps and unset/example/override LOG_* forwarding passed")
PY
