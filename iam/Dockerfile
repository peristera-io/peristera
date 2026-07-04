# Stub relying party — first (dev) entry of the tenant app catalog.
# Build context is the REPO ROOT: iam depends on the sibling lib/ module
# (replace => ../lib), so lib/ must be in the context.
#   docker build -f iam/Dockerfile -t peristera-stub:dev .
#   k3d image import -c peristera-dev peristera-stub:dev
FROM golang:1.26 AS build
WORKDIR /src
COPY lib/ ./lib/
COPY iam/ ./iam/
WORKDIR /src/iam
RUN CGO_ENABLED=0 go build -o /stub ./cmd/stub

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /stub /stub
ENTRYPOINT ["/stub"]
