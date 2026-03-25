FROM debian:bookworm-slim

RUN apt-get update \
    && apt-get install -y --no-install-recommends curl unzip ca-certificates \
    && rm -rf /var/lib/apt/lists/*

# Same TigerBeetle binary — uses `tigerbeetle amqp` sub-command for CDC
RUN curl -fsSL -o /tmp/tigerbeetle.zip https://linux.tigerbeetle.com \
    && unzip /tmp/tigerbeetle.zip -d /usr/local/bin \
    && rm /tmp/tigerbeetle.zip \
    && chmod +x /usr/local/bin/tigerbeetle

COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

ENTRYPOINT ["/entrypoint.sh"]
