# syntax=docker/dockerfile:1.7
FROM node:22-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --ignore-scripts
COPY web/ ./
RUN npm run build

FROM golang:1.26-alpine AS backend
ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_TIME=unknown
WORKDIR /src
RUN apk add --no-cache ca-certificates git
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ cmd/
COPY internal/ internal/
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath \
    -ldflags "-s -w -X github.com/hkjang/Vendra/internal/httpapi.Version=${VERSION} -X github.com/hkjang/Vendra/internal/httpapi.Commit=${COMMIT} -X github.com/hkjang/Vendra/internal/httpapi.BuildTime=${BUILD_TIME}" \
    -o /out/vendra ./cmd/vendra

FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata && addgroup -S vendra && adduser -S -G vendra -h /app vendra
WORKDIR /app
# Ownership is set as the files are copied. A recursive chown afterwards
# rewrote every file's metadata, and a layer records a whole copy of anything it
# touches — the binary and the whole frontend were shipped twice, which was
# thirteen of the image's thirty-seven megabytes.
COPY --from=backend --chown=vendra:vendra /out/vendra /app/vendra
COPY --from=web --chown=vendra:vendra /src/web/dist /app/web/dist
RUN mkdir -p /var/lib/vendra/documents && chown -R vendra:vendra /var/lib/vendra
USER vendra
EXPOSE 8080
VOLUME ["/var/lib/vendra/documents"]
HEALTHCHECK --interval=30s --timeout=3s --start-period=15s --retries=3 CMD wget -q -O - http://127.0.0.1:8080/health/ready >/dev/null || exit 1
ENTRYPOINT ["/app/vendra"]
