FROM alpine:3.19

RUN apk add --no-cache bash aws-cli

COPY backup.sh /backup.sh
RUN chmod +x /backup.sh

ENTRYPOINT ["/backup.sh"]
