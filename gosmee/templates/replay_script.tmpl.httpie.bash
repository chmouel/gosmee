#!/usr/bin/env bash
# Replay script using httpie
#
# Usage: ./script.sh [OPTIONS] [TARGET_URL]
# Options:
#   -l, --local     Use local debug URL ({{ .LocalDebugURL }})
#   -t, --target    Specify target URL
#   -h, --help      Show this help message
#   -v, --verbose   Enable verbose output
#
set -euo pipefail
cd $(dirname $(readlink -f $0))

# Default values
targetURL="{{ .TargetURL }}"
verbose=false
use_local=false

# Parse command line options
TEMP=$(getopt -o 'lhvt:' --long 'local,help,verbose,target:' -n "$(basename "$0")" -- "$@")
if [[ $? -ne 0 ]]; then
    echo "Failed to parse options" >&2
    exit 1
fi

eval set -- "$TEMP"
unset TEMP

while true; do
    case "$1" in
        '-l'|'--local')
            use_local=true
            shift
            continue
            ;;
        '-t'|'--target')
            targetURL="$2"
            shift 2
            continue
            ;;
        '-h'|'--help')
            echo "Usage: $(basename "$0") [OPTIONS] [TARGET_URL]"
            echo "Replay webhook payload to target URL using httpie"
            echo ""
            echo "Options:"
            echo "  -l, --local     Use local debug URL ({{ .LocalDebugURL }})"
            echo "  -t, --target    Specify target URL"
            echo "  -h, --help      Show this help message"
            echo "  -v, --verbose   Enable verbose output"
            echo ""
            echo "Environment variables:"
            echo "  GOSMEE_DEBUG_SERVICE  Alternative target URL"
            echo ""
            echo "Examples:"
            echo "  $(basename "$0")                           # Use default target URL"
            echo "  $(basename "$0") -l                        # Use local debug URL"
            echo "  $(basename "$0") -t http://example.com:8080 # Use specific target URL"
            echo "  $(basename "$0") http://example.com:8080   # Use custom URL"
            echo ""
            echo "Note: This script requires httpie to be installed"
            exit 0
            ;;
        '-v'|'--verbose')
            verbose=true
            shift
            continue
            ;;
        '--')
            shift
            break
            ;;
        *)
            echo "Internal error parsing options" >&2
            exit 1
            ;;
    esac
done

# Handle positional arguments
if [[ $# -gt 1 ]]; then
    echo "Error: Too many arguments" >&2
    echo "Use -h or --help for usage information" >&2
    exit 1
elif [[ $# -eq 1 ]]; then
    targetURL="$1"
fi

# Apply local flag or environment variable
if [[ "$use_local" == "true" ]]; then
    targetURL="{{ .LocalDebugURL }}"
elif [[ -n "${GOSMEE_DEBUG_SERVICE:-}" ]]; then
    targetURL="${GOSMEE_DEBUG_SERVICE}"
fi

# Set verbose flag for httpie if requested
http_flags="--print=HhBb"
if [[ "$verbose" == "true" ]]; then
    http_flags+=" --verbose"
    set -x
fi

echo "Replaying webhook to: $targetURL"
http -F $http_flags POST "${targetURL}" Content-Type:"{{ .ContentType }}" {{ .Headers }} < ./{{ .FileBase }}.json
