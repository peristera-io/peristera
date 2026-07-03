# Stub relying party — the first (dev) entry of the tenant app catalog.
#   docker build -t peristera-stub:dev iam/
#   k3d image import -c peristera-dev peristera-stub:dev
FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /stub ./cmd/stub

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /stub /stub
ENTRYPOINT ["/stub"]
