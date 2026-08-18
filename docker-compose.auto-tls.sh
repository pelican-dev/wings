#!/bin/sh

set -eu

script_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
compose_file="$script_dir/docker-compose.auto-tls.example.yml"
env_file="${PWD}/.env"
check_only=false
expect_env_file=false

for argument do
    if [ "$expect_env_file" = true ]; then
        env_file=$argument
        expect_env_file=false
        continue
    fi

    case $argument in
        --env-file)
            expect_env_file=true
            ;;
        --env-file=*)
            env_file=${argument#--env-file=}
            ;;
        --check)
            check_only=true
            ;;
    esac
done

if [ "$expect_env_file" = true ]; then
    printf '%s\n' 'Automatic TLS preflight failed: --env-file requires a path.' >&2
    exit 1
fi

read_env_value() {
    variable_name=$1

    if [ ! -f "$env_file" ]; then
        return
    fi

    awk -v wanted="$variable_name" '
        function trim(value) {
            sub(/^[[:space:]]+/, "", value)
            sub(/[[:space:]]+$/, "", value)
            return value
        }

        {
            line = $0
            sub(/\r$/, "", line)
            line = trim(line)

            if (line == "" || substr(line, 1, 1) == "#") {
                next
            }

            sub(/^export[[:space:]]+/, "", line)
            separator = index(line, "=")

            if (separator == 0) {
                next
            }

            key = trim(substr(line, 1, separator - 1))

            if (key != wanted) {
                next
            }

            value = trim(substr(line, separator + 1))

            if (length(value) >= 2 &&
                ((substr(value, 1, 1) == "\"" && substr(value, length(value), 1) == "\"") ||
                 (substr(value, 1, 1) == "\047" && substr(value, length(value), 1) == "\047"))) {
                value = substr(value, 2, length(value) - 2)
            } else {
                sub(/[[:space:]]+#.*/, "", value)
                value = trim(value)
            }

            result = value
            found = 1
        }

        END {
            if (found) {
                print result
            }
        }
    ' "$env_file"
}

if [ "${WINGS_API_PORT+x}" = x ]; then
    api_port=$WINGS_API_PORT
else
    api_port=$(read_env_value WINGS_API_PORT)
fi

if [ "${WINGS_SFTP_PORT+x}" = x ]; then
    sftp_port=$WINGS_SFTP_PORT
else
    sftp_port=$(read_env_value WINGS_SFTP_PORT)
fi

validate_port() {
    variable_name=$1
    port=$2

    if [ -z "$port" ]; then
        printf 'Automatic TLS preflight failed: %s is required.\n' "$variable_name" >&2
        exit 1
    fi

    case $port in
        *[!0-9]*)
            printf 'Automatic TLS preflight failed: %s must be a numeric port from 1 through 65535.\n' "$variable_name" >&2
            exit 1
            ;;
    esac

    if ! awk -v port="$port" 'BEGIN { exit !(port >= 1 && port <= 65535) }'; then
        printf 'Automatic TLS preflight failed: %s must be a numeric port from 1 through 65535.\n' "$variable_name" >&2
        exit 1
    fi

    if awk -v port="$port" 'BEGIN { exit !(port == 80) }'; then
        printf 'Automatic TLS preflight failed: %s cannot use port 80; Wings reserves it for the ACME HTTP-01 challenge.\n' "$variable_name" >&2
        exit 1
    fi
}

validate_port WINGS_API_PORT "$api_port"
validate_port WINGS_SFTP_PORT "$sftp_port"

if awk -v api="$api_port" -v sftp="$sftp_port" 'BEGIN { exit !(api == sftp) }'; then
    printf '%s\n' 'Automatic TLS preflight failed: WINGS_API_PORT and WINGS_SFTP_PORT must be different.' >&2
    exit 1
fi

printf 'Automatic TLS port preflight passed (API %s, SFTP %s).\n' "$api_port" "$sftp_port"

if [ "$check_only" = true ]; then
    exit 0
fi

exec docker compose -f "$compose_file" "$@"
