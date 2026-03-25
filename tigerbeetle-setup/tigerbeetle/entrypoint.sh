#!/bin/sh
set -e

CLUSTER="${TB_CLUSTER:-0}"
REPLICA="${TB_REPLICA:-0}"
REPLICA_COUNT="${TB_REPLICA_COUNT:-1}"
DATA_FILE="/data/${CLUSTER}_${REPLICA}.tigerbeetle"
ADDRESSES="${TB_ADDRESSES:-0.0.0.0:3000}"
DEVELOPMENT="${TB_DEVELOPMENT:-true}"

DEV_FLAG=""
if [ "$DEVELOPMENT" = "true" ]; then
    DEV_FLAG="--development"
fi

if [ ! -f "$DATA_FILE" ]; then
    echo "Data file not found. Formatting new data file: $DATA_FILE"
    tigerbeetle format \
        --cluster="$CLUSTER" \
        --replica="$REPLICA" \
        --replica-count="$REPLICA_COUNT" \
        $DEV_FLAG \
        "$DATA_FILE"
    echo "Format complete."
else
    echo "Data file found: $DATA_FILE"
fi

echo "Starting TigerBeetle (cluster=$CLUSTER, replica=$REPLICA, addresses=$ADDRESSES)"
exec tigerbeetle start \
    --addresses="$ADDRESSES" \
    $DEV_FLAG \
    "$DATA_FILE"
