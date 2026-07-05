# Peristera Kamara — the per-tenant file store.
# Build context is the REPO ROOT (depends on the sibling lib/ module):
#   docker build -f kamara/Dockerfile -t peristera-kamara:dev .
#   k3d image import -c peristera-dev peristera-kamara:dev
FROM golang:1.26 AS build
WORKDIR /src
COPY lib/ ./lib/
COPY kamara/ ./kamara/
WORKDIR /src/kamara
RUN CGO_ENABLED=0 go build -o /kamara ./cmd/kamara

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /kamara /kamara
ENTRYPOINT ["/kamara"]
