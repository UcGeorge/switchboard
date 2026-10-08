FROM golang:1.26.3-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /switchboard ./cmd/switchboard
FROM alpine:3.23
RUN apk add --no-cache ca-certificates && addgroup -g 10001 switchboard && adduser -D -u 10001 -G switchboard switchboard && mkdir /data && chown switchboard:switchboard /data
COPY --from=build /switchboard /usr/local/bin/switchboard
USER switchboard
VOLUME ["/data"]
EXPOSE 8080
ENTRYPOINT ["switchboard"]
CMD ["serve", "--headless", "--addr", "0.0.0.0:8080", "--db", "/data/switchboard.db"]
