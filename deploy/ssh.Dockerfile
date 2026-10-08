FROM alpine:3.23
RUN apk add --no-cache openssh-client python3 tar curl ca-certificates
WORKDIR /workspace
ENTRYPOINT []
