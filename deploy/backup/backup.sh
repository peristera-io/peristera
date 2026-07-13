#!/bin/sh
# Interim tenant-data backup (#59/#77). Two independent steps, both driven by
# env so the same image serves the per-tenant blob job and the platform
# secret-escrow job:
#
#   BLOB_DIR set    -> rclone copy of the content-addressed chunk store to
#                      s3://$BACKUP_BUCKET/$BACKUP_PREFIX/blobs. Copy, never
#                      sync --delete: chunks are immutable and content-
#                      addressed, so a superset is always restorable and a
#                      mid-GC race can only add, never lose. `.tmp-*` files
#                      are kamara's not-yet-renamed writes — skip them.
#   ESCROW_DIR set  -> tar of the mounted Secret dirs, encrypted to the age
#                      recipient (an X25519 public key; the private key lives
#                      OUTSIDE the cluster), uploaded as secrets.tar.gz.age.
#                      Without this a restored blob store is undecryptable.
#
# Required: BACKUP_BUCKET, BACKUP_PREFIX, BACKUP_ENDPOINT, BACKUP_REGION,
# ACCESS_KEY_ID, ACCESS_SECRET_KEY. AGE_RECIPIENT is required when ESCROW_DIR
# is set — missing it fails the job loudly rather than silently skipping the
# half of the backup that makes the other half usable.
set -eu

export RCLONE_S3_PROVIDER="${RCLONE_S3_PROVIDER:-Scaleway}"
export RCLONE_S3_ENDPOINT="${BACKUP_ENDPOINT:?}"
export RCLONE_S3_REGION="${BACKUP_REGION:?}"
export RCLONE_S3_ACCESS_KEY_ID="${ACCESS_KEY_ID:?}"
export RCLONE_S3_SECRET_ACCESS_KEY="${ACCESS_SECRET_KEY:?}"

DEST=":s3:${BACKUP_BUCKET:?}/${BACKUP_PREFIX:?}"

if [ -n "${BLOB_DIR:-}" ]; then
  # --immutable: a content-addressed chunk never legitimately changes, so a
  # source file that differs from its bucket copy is corruption or tampering
  # (e.g. ransomware rewriting the PVC) — refuse to overwrite the good copy
  # and fail the job loudly instead of poisoning the only off-node backup.
  # New chunks still upload; the bucket also has versioning as backstop.
  rclone copy --s3-no-check-bucket --immutable --exclude '.tmp-*' \
    "$BLOB_DIR" "${DEST}/blobs"
  echo "backup: blobs copied to ${DEST}/blobs"
fi

if [ -n "${ESCROW_DIR:-}" ]; then
  : "${AGE_RECIPIENT:?AGE_RECIPIENT required when ESCROW_DIR is set}"
  # -h dereferences the ..data symlinks Kubernetes uses in Secret mounts.
  tar -C "$ESCROW_DIR" -chzf - . | age -e -r "$AGE_RECIPIENT" |
    rclone rcat --s3-no-check-bucket "${DEST}/secrets.tar.gz.age"
  echo "backup: secrets escrowed to ${DEST}/secrets.tar.gz.age"
fi

# Optional dead-man's switch (e.g. healthchecks.io): ping only after every
# configured step succeeded, so a missing ping means a broken backup.
if [ -n "${HEARTBEAT_URL:-}" ]; then
  wget -q -O /dev/null "$HEARTBEAT_URL" || echo "backup: heartbeat ping failed (non-fatal)"
fi
