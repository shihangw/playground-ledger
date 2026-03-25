FROM debian:bookworm-slim

RUN apt-get update \
    && apt-get install -y --no-install-recommends curl unzip ca-certificates \
    && rm -rf /var/lib/apt/lists/*

RUN curl -fsSL -o /tmp/tigerbeetle.zip https://linux.tigerbeetle.com \
    && unzip /tmp/tigerbeetle.zip -d /usr/local/bin \
    && rm /tmp/tigerbeetle.zip \
    && chmod +x /usr/local/bin/tigerbeetle

VOLUME /data
EXPOSE 3000

COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

ENTRYPOINT ["/entrypoint.sh"]
