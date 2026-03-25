#!/bin/sh
set -e

TB_ADDRESSES="${TB_ADDRESSES:-tigerbeetle:3000}"
TB_CLUSTER="${TB_CLUSTER:-0}"
AMQP_HOST="${AMQP_HOST:-rabbitmq}"
AMQP_PORT="${AMQP_PORT:-5672}"
AMQP_VHOST="${AMQP_VHOST:-/}"
AMQP_USER="${AMQP_USER:-guest}"
AMQP_PASS="${AMQP_PASS:-guest}"
AMQP_EXCHANGE="${AMQP_EXCHANGE:-tigerbeetle}"
EVENT_COUNT_MAX="${EVENT_COUNT_MAX:-2730}"
IDLE_INTERVAL_MS="${IDLE_INTERVAL_MS:-1000}"

# Optional: resume from a specific timestamp (nanoseconds)
# Set TIMESTAMP_LAST to replay events from that point
TIMESTAMP_LAST_FLAG=""
if [ -n "$TIMESTAMP_LAST" ]; then
    TIMESTAMP_LAST_FLAG="--timestamp-last=$TIMESTAMP_LAST"
fi

echo "Waiting for TigerBeetle at $TB_ADDRESSES..."
# tigerbeetle amqp requires an IP address, not a hostname — resolve it
TB_HOST=$(echo "$TB_ADDRESSES" | cut -d: -f1)
TB_PORT=$(echo "$TB_ADDRESSES" | cut -d: -f2)
sleep 5
TB_IP=$(getent hosts "$TB_HOST" | awk '{print $1}')
if [ -n "$TB_IP" ]; then
    TB_ADDRESSES="${TB_IP}:${TB_PORT}"
    echo "Resolved $TB_HOST -> $TB_IP"
fi

# Also resolve the AMQP host
AMQP_IP=$(getent hosts "$AMQP_HOST" | awk '{print $1}')
if [ -n "$AMQP_IP" ]; then
    echo "Resolved $AMQP_HOST -> $AMQP_IP"
    AMQP_HOST="$AMQP_IP"
fi

echo "Starting TigerBeetle CDC -> RabbitMQ stream"
echo "  Cluster:  $TB_CLUSTER"
echo "  TB:       $TB_ADDRESSES"
echo "  AMQP:     $AMQP_USER@$AMQP_HOST:$AMQP_PORT$AMQP_VHOST"
echo "  Exchange: $AMQP_EXCHANGE"

exec tigerbeetle amqp \
    --addresses="$TB_ADDRESSES" \
    --cluster="$TB_CLUSTER" \
    --host="$AMQP_HOST:$AMQP_PORT" \
    --vhost="$AMQP_VHOST" \
    --user="$AMQP_USER" \
    --password="$AMQP_PASS" \
    --publish-exchange="$AMQP_EXCHANGE" \
    --event-count-max="$EVENT_COUNT_MAX" \
    --idle-interval-ms="$IDLE_INTERVAL_MS" \
    $TIMESTAMP_LAST_FLAG
