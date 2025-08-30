#!/usr/bin/env bash
#!/usr/bin/env bash
set -euo pipefail

# Config (override via env or edit here)
COUNT=${COUNT:-10}
PREFIX=${PREFIX:-preview}       # namespaces will be PREFIX-1 .. PREFIX-10
START=${START:-1}
TTL=${TTL:-20m}
SWEEPER_DOMAIN=${SWEEPER_DOMAIN:-preview-sweeper.maxsauce.com}

LABEL_KEY="${SWEEPER_DOMAIN}/enabled"
ANNOTATION_KEY="${SWEEPER_DOMAIN}/ttl"

for ((i=START; i<START+COUNT; i++)); do
  ns="${PREFIX}-default-${i}"
  # Create (or update) the namespace with the desired metadata
  cat <<YAML | kubectl apply -f -
apiVersion: v1
kind: Namespace
metadata:
  name: ${ns}
  labels:
    "${LABEL_KEY}": "true"
YAML
  echo "Applied namespace ${ns}"
done
