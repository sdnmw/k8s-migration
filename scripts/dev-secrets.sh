#!/bin/sh
set -eu

secret_dir="${1:-.data/secrets}"
umask 077
mkdir -p "$secret_dir"

if [ ! -s "$secret_dir/master-key" ]; then
  openssl rand -base64 32 > "$secret_dir/master-key"
fi

if [ ! -s "$secret_dir/admin-password" ]; then
  openssl rand -base64 18 > "$secret_dir/admin-password"
fi

printf 'Development administrator: admin\n'
printf 'Development password: '
tr -d '\n' < "$secret_dir/admin-password"
printf '\nSecrets directory: %s\n' "$secret_dir"
