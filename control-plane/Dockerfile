# Control plane — controller + UI/API in one binary (ADR-0008).
#   docker build -t peristera-control-plane:dev control-plane/
#   k3d image import -c peristera-dev peristera-control-plane:dev
FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /control-plane ./cmd/controller

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /control-plane /control-plane
ENTRYPOINT ["/control-plane"]
