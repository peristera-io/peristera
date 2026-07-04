# Peristera Ergonomos — the task app.
# Build context is the REPO ROOT (depends on the sibling lib/ module):
#   docker build -f ergonomos/Dockerfile -t peristera-ergonomos:dev .
#   k3d image import -c peristera-dev peristera-ergonomos:dev
FROM golang:1.26 AS build
WORKDIR /src
COPY lib/ ./lib/
COPY ergonomos/ ./ergonomos/
WORKDIR /src/ergonomos
RUN CGO_ENABLED=0 go build -o /ergonomos ./cmd/ergonomos

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /ergonomos /ergonomos
ENTRYPOINT ["/ergonomos"]
