#!/bin/sh
set -eu

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
caddyfile="$repo_root/deploy/Caddyfile"
active_config=$(sed 's/[[:space:]]*#.*$//' "$caddyfile")

if ! printf '%s\n' "$active_config" | grep -Eq 'path_regexp[[:space:]]+hashed_assets'; then
	echo "Caddyfile must scope immutable caching to fingerprinted build assets" >&2
	exit 1
fi

if ! printf '%s\n' "$active_config" | grep -Eq 'Cache-Control[[:space:]]+"public, max-age=31536000, immutable"'; then
	echo "Caddyfile must retain immutable caching for fingerprinted assets" >&2
	exit 1
fi

if ! printf '%s\n' "$active_config" | grep -Eq '@brandAssets[[:space:]]+path[[:space:]].*favicon'; then
	echo "Caddyfile must keep a dedicated stable-brand asset matcher" >&2
	exit 1
fi

if ! printf '%s\n' "$active_config" | grep -Eq 'Cache-Control[[:space:]]+"no-cache"'; then
	echo "Caddyfile must force stable brand assets to revalidate" >&2
	exit 1
fi

if ! printf '%s\n' "$active_config" | grep -Eq '^[[:space:]]*reverse_proxy[[:space:]]+localhost:8080'; then
	echo "Caddyfile must continue proxying all application routes to localhost:8080" >&2
	exit 1
fi

echo "Caddyfile keeps fingerprinted assets immutable, brand assets revalidated, and reverse_proxy routing"
