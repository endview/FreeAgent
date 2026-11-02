#!/bin/sh
set -eu

freeagent_launcher=$(readlink -f -- "$0")
freeagent_package=$(CDPATH= cd -- "$(dirname -- "$freeagent_launcher")" && pwd -P)
freeagent_cli="$freeagent_package/bin/freeagent"

if [ "$#" -eq 0 ]; then
    set -- web
fi

case "$1" in
    web|chat)
        freeagent_data="$freeagent_package/data/Harness-v7"
        freeagent_next_data=false
        for freeagent_argument in "$@"; do
            if [ "$freeagent_next_data" = true ]; then
                freeagent_data=$freeagent_argument
                freeagent_next_data=false
            else
                case "$freeagent_argument" in
                    -h|--help) exec "$freeagent_cli" "$@" ;;
                    --data-dir) freeagent_next_data=true ;;
                    --data-dir=*) freeagent_data=${freeagent_argument#--data-dir=} ;;
                esac
            fi
        done
        if [ "$freeagent_next_data" = true ] || [ -z "$freeagent_data" ]; then
            exec "$freeagent_cli" "$@"
        fi
        if [ ! -f "$freeagent_data/config.json" ]; then
            mkdir -p -- "$freeagent_package/workspace"
            "$freeagent_cli" init --data-dir "$freeagent_data" --workspace "$freeagent_package/workspace" >/dev/null
        fi
        ;;
esac

exec "$freeagent_cli" "$@"
