FROM golang:1.16.0-buster as build

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -o ./consul-backup-s3 ./cmd/consul-backup-s3


FROM ubuntu

RUN apt-get update \
    && apt-get install -y ca-certificates \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/*

COPY --from=build /app/consul-backup-s3 /usr/local/bin/

ENTRYPOINT ["/usr/local/bin/consul-backup-s3"]
