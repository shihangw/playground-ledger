#!/usr/bin/env bash
# setup-alloydb.sh — Create AlloyDB cluster/instance if not exists, then save DSN to Secret Manager.
#
# Usage:
#   ./scripts/setup-alloydb.sh [--cpu-count <n>] [--project <proj>] [--region <r>] [--network <net>]
#
# Optional overrides (defaults match README):
#   LEDGER_GCP_PROJECT       (default: sw-playground-ledger)
#   ALLOYDB_CLUSTER   (default: ledger-bench)
#   ALLOYDB_REGION    (default: us-central1)
#   ALLOYDB_INSTANCE  (default: primary)
#   ALLOYDB_CPU_COUNT (default: 2)
#   ALLOYDB_NETWORK   (default: default)
#
# The postgres password is auto-generated on first run and stored in Secret Manager
# as ALLOYDB_POSTGRES_PASSWORD. Subsequent runs reuse it — no manual password needed.

set -euo pipefail

# ---------------------------------------------------------------------------
# Defaults
# ---------------------------------------------------------------------------
LEDGER_GCP_PROJECT="${LEDGER_GCP_PROJECT:?LEDGER_GCP_PROJECT must be set (e.g. export LEDGER_GCP_PROJECT=sw-playground-ledger)}"
ALLOYDB_CLUSTER="${ALLOYDB_CLUSTER:-ledger-bench}"
ALLOYDB_REGION="${ALLOYDB_REGION:-us-central1}"
ALLOYDB_INSTANCE="${ALLOYDB_INSTANCE:-primary}"
ALLOYDB_CPU_COUNT="${ALLOYDB_CPU_COUNT:-2}"
ALLOYDB_NETWORK="${ALLOYDB_NETWORK:-default}"

# ---------------------------------------------------------------------------
# Arg parsing
# ---------------------------------------------------------------------------
while [[ $# -gt 0 ]]; do
  case "$1" in
    --cpu-count) ALLOYDB_CPU_COUNT="$2"; shift 2 ;;
    --project)   LEDGER_GCP_PROJECT="$2";       shift 2 ;;
    --region)    ALLOYDB_REGION="$2";    shift 2 ;;
    --network)   ALLOYDB_NETWORK="$2";   shift 2 ;;
    *) echo "Unknown flag: $1" >&2; exit 1 ;;
  esac
done

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------
log() { echo "[setup-alloydb] $*"; }
gcloud_alloydb() { gcloud alloydb "$@" --project="$LEDGER_GCP_PROJECT"; }

upsert_secret() {
  local name="$1"
  local value="$2"
  local exists
  exists=$(gcloud secrets describe "$name" \
    --project="$LEDGER_GCP_PROJECT" --format="value(name)" 2>/dev/null || true)
  if [[ -z "$exists" ]]; then
    log "Creating secret '$name'..."
    printf '%s' "$value" | gcloud secrets create "$name" \
      --project="$LEDGER_GCP_PROJECT" --replication-policy=automatic --data-file=- --quiet
  else
    log "Secret '$name' exists. Adding new version..."
    printf '%s' "$value" | gcloud secrets versions add "$name" \
      --project="$LEDGER_GCP_PROJECT" --data-file=- --quiet
  fi
}

# ---------------------------------------------------------------------------
# 1. Enable required APIs
# ---------------------------------------------------------------------------
log "Enabling required GCP APIs..."
gcloud services enable alloydb.googleapis.com secretmanager.googleapis.com \
  --project="$LEDGER_GCP_PROJECT" --quiet

# ---------------------------------------------------------------------------
# 2. Generate or reuse postgres password
# ---------------------------------------------------------------------------
ALLOYDB_PASSWORD_SECRET="ALLOYDB_POSTGRES_PASSWORD"

ALLOYDB_PASSWORD=$(gcloud secrets versions access latest \
  --secret="$ALLOYDB_PASSWORD_SECRET" \
  --project="$LEDGER_GCP_PROJECT" 2>/dev/null || true)

if [[ -z "$ALLOYDB_PASSWORD" ]]; then
  log "Generating a new random password..."
  ALLOYDB_PASSWORD=$(openssl rand -base64 24 | tr -d '/+=')
  upsert_secret "$ALLOYDB_PASSWORD_SECRET" "$ALLOYDB_PASSWORD"
  log "Password saved to secret '$ALLOYDB_PASSWORD_SECRET'."
else
  log "Reusing existing password from secret '$ALLOYDB_PASSWORD_SECRET'."
fi

# ---------------------------------------------------------------------------
# 3. Create AlloyDB cluster if it doesn't exist
# ---------------------------------------------------------------------------
log "Checking AlloyDB cluster '$ALLOYDB_CLUSTER'..."
CLUSTER_EXISTS=$(gcloud_alloydb clusters list \
  --region="$ALLOYDB_REGION" \
  --filter="name~/$ALLOYDB_CLUSTER$" \
  --format="value(name)" 2>/dev/null || true)

if [[ -z "$CLUSTER_EXISTS" ]]; then
  log "Cluster not found. Creating '$ALLOYDB_CLUSTER' in $ALLOYDB_REGION..."
  gcloud_alloydb clusters create "$ALLOYDB_CLUSTER" \
    --region="$ALLOYDB_REGION" \
    --network="$ALLOYDB_NETWORK" \
    --database-version=POSTGRES_18 \
    --password="$ALLOYDB_PASSWORD" \
    --quiet
  log "Cluster created."
else
  log "Cluster '$ALLOYDB_CLUSTER' already exists. Skipping creation."
fi

# ---------------------------------------------------------------------------
# 4. Create AlloyDB primary instance if it doesn't exist
# ---------------------------------------------------------------------------
log "Checking AlloyDB instance '$ALLOYDB_INSTANCE'..."
INSTANCE_EXISTS=$(gcloud_alloydb instances list \
  --cluster="$ALLOYDB_CLUSTER" \
  --region="$ALLOYDB_REGION" \
  --filter="name~/$ALLOYDB_INSTANCE$" \
  --format="value(name)" 2>/dev/null || true)

if [[ -z "$INSTANCE_EXISTS" ]]; then
  log "Instance not found. Creating '$ALLOYDB_INSTANCE' (cpu-count=$ALLOYDB_CPU_COUNT)..."
  gcloud_alloydb instances create "$ALLOYDB_INSTANCE" \
    --cluster="$ALLOYDB_CLUSTER" \
    --region="$ALLOYDB_REGION" \
    --instance-type=PRIMARY \
    --availability-type=ZONAL \
    --cpu-count="$ALLOYDB_CPU_COUNT" \
    --quiet
  log "Instance created."
else
  log "Instance '$ALLOYDB_INSTANCE' already exists. Skipping creation."
fi

# ---------------------------------------------------------------------------
# 5. Set postgres password on the cluster
# ---------------------------------------------------------------------------
log "Setting postgres password on cluster..."
gcloud_alloydb users set-password postgres \
  --cluster="$ALLOYDB_CLUSTER" \
  --region="$ALLOYDB_REGION" \
  --password="$ALLOYDB_PASSWORD" \
  --quiet
log "Password set."

# ---------------------------------------------------------------------------
# 6. Fetch private IP and save ALLOYDB_DSN
# ---------------------------------------------------------------------------
log "Fetching private IP of instance '$ALLOYDB_INSTANCE'..."
PRIVATE_IP=$(gcloud_alloydb instances describe "$ALLOYDB_INSTANCE" \
  --cluster="$ALLOYDB_CLUSTER" \
  --region="$ALLOYDB_REGION" \
  --format="value(ipAddress)")

if [[ -z "$PRIVATE_IP" ]]; then
  echo "Error: Could not retrieve private IP for instance '$ALLOYDB_INSTANCE'." >&2
  exit 1
fi
log "Private IP: $PRIVATE_IP"

ALLOYDB_DSN="postgres://postgres:${ALLOYDB_PASSWORD}@${PRIVATE_IP}:5432/postgres?sslmode=require"
upsert_secret "ALLOYDB_DSN" "$ALLOYDB_DSN"

# ---------------------------------------------------------------------------
# 7. Create placeholder secrets for other required secrets (if missing)
# ---------------------------------------------------------------------------
for SECRET in CRDB_DSN; do
  EXISTS=$(gcloud secrets describe "$SECRET" \
    --project="$LEDGER_GCP_PROJECT" --format="value(name)" 2>/dev/null || true)
  if [[ -z "$EXISTS" ]]; then
    log "Creating placeholder secret '$SECRET' (update with real value later)..."
    printf '%s' "PLACEHOLDER" | gcloud secrets create "$SECRET" \
      --project="$LEDGER_GCP_PROJECT" --replication-policy=automatic --data-file=- --quiet
    log "  -> gcloud secrets versions add $SECRET --data-file=- --project=$LEDGER_GCP_PROJECT"
  else
    log "Secret '$SECRET' already exists. Skipping."
  fi
done

# ---------------------------------------------------------------------------
# 8. Run migrations
# ---------------------------------------------------------------------------
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
GO_DIR="$SCRIPT_DIR/../go"

log "Running migrations against AlloyDB..."
ALLOYDB_DSN="$ALLOYDB_DSN" DB_BACKEND=alloydb go run "$GO_DIR/cmd/migrate/main.go" -cmd up
log "Migrations complete."

# ---------------------------------------------------------------------------
# Done
# ---------------------------------------------------------------------------
log ""
log "AlloyDB setup complete."
log "  Cluster:    $ALLOYDB_CLUSTER ($ALLOYDB_REGION)"
log "  Instance:   $ALLOYDB_INSTANCE"
log "  Private IP: $PRIVATE_IP"
log "  Secrets saved to project $LEDGER_GCP_PROJECT:"
log "    ALLOYDB_POSTGRES_PASSWORD  (generated password)"
log "    ALLOYDB_DSN                (connection string)"
log ""
log "Start the API:"
log "  DB_BACKEND=alloydb go run go/cmd/api/main.go"
