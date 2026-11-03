FROM alpine:3.20

RUN apk add --no-cache bash ca-certificates coreutils curl docker-cli docker-cli-compose findutils git gzip jq openssh-client tar

WORKDIR /usr/local/lib/xiass
COPY xiass-update.sh xiass-runtime-start.sh xiass-backup.sh xiass-runtime-export.sh xiass-runtime-restore.sh ./
COPY xiass-updater/xiass-updater-entrypoint.sh /usr/local/bin/xiass-updater
RUN chmod 0755 /usr/local/bin/xiass-updater ./xiass-update.sh ./xiass-runtime-start.sh ./xiass-backup.sh ./xiass-runtime-export.sh ./xiass-runtime-restore.sh

ENTRYPOINT ["/usr/local/bin/xiass-updater"]
