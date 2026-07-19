# syntax=docker/dockerfile:1
FROM --platform=$BUILDPLATFORM golang:1.24-alpine AS build
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY main.go ./
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /out/switch-cli-exporter .

FROM scratch
COPY --from=build /out/switch-cli-exporter /switch-cli-exporter
USER 65532:65532
EXPOSE 9808
ENTRYPOINT ["/switch-cli-exporter"]

