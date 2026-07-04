# Control plane — controller + UI/API in one binary (ADR-0008).
# Build context is the REPO ROOT: control-plane depends on the sibling
# lib/ module (replace => ../lib), so lib/ must be in the context.
#   docker build -f control-plane/Dockerfile -t peristera-control-plane:dev .
#   k3d image import -c peristera-dev peristera-control-plane:dev
FROM golang:1.26 AS build
WORKDIR /src
COPY lib/ ./lib/
COPY control-plane/ ./control-plane/
WORKDIR /src/control-plane
RUN CGO_ENABLED=0 go build -o /control-plane ./cmd/controller

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /control-plane /control-plane
ENTRYPOINT ["/control-plane"]
