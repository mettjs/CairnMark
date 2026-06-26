# Multi-stage build. Migrations are embedded in the binary (migrations.FS), so
# the runtime image needs only the static binary + CA certs.
FROM golang:1.26-alpine AS build
WORKDIR /src

# Cache module downloads.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /out/cairnmark ./cmd/server

FROM alpine:3.20
RUN apk add --no-cache ca-certificates \
    && adduser -D -u 10001 cairnmark
USER cairnmark
COPY --from=build /out/cairnmark /usr/local/bin/cairnmark
EXPOSE 8080
ENTRYPOINT ["cairnmark"]
