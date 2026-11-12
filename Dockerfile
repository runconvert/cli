# Remote MCP server image (mcp.runconvert.com): `runconvert mcp --http :8080`.
# Built for linux/amd64 and linux/arm64 by .github/workflows/release.yml.
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
ARG TARGETOS TARGETARCH VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o /out/runconvert ./cmd/runconvert

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/runconvert /usr/local/bin/runconvert
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/runconvert", "mcp", "--http", ":8080"]
