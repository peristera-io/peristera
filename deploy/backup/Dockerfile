# The interim backup job image (#59/#77): rclone pushes a tenant's
# content-addressed blob store to Object Storage, age escrows mounted Secrets
# (the Kamara DEK, the platform's Zitadel masterkey + admin-client key) so a
# restored database is actually decryptable. Retired when #21 moves blobs to
# S3 natively.
FROM alpine:3.22
RUN apk add --no-cache rclone age
COPY deploy/backup/backup.sh /usr/local/bin/backup.sh
USER 65532:65532
ENTRYPOINT ["/bin/sh", "/usr/local/bin/backup.sh"]
