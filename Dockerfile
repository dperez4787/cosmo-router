# Custom Cosmo router build: the upstream router plus this repo's modules.
# Run scripts/compose.sh first — the image bakes in execution-config.json.
FROM golang:1.25 AS build
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY modules ./modules
RUN CGO_ENABLED=0 go build -trimpath -o /router ./cmd/router

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /router /router
COPY config/config.yaml /config/config.yaml
COPY execution-config.json /config/execution-config.json
ENV CONFIG_PATH=/config/config.yaml
EXPOSE 3002
ENTRYPOINT ["/router"]
