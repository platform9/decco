FROM golang:1.14 as builder
WORKDIR /app

# Copy the Go Modules manifests
COPY go.mod go.mod
COPY go.sum go.sum

# Cache deps before building and copying source so that we don't need to re-download as much
# and so that source changes don't invalidate our downloaded layer
RUN go mod download

COPY . .

# If Decco is passed in from the host, skip the build.
RUN stat decco-operator || CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o decco-operator ./cmd/operator

FROM debian:latest

COPY --from=builder /app/decco-operator /usr/local/bin/

RUN apt-get -y update
RUN apt-get -y install ca-certificates

ENTRYPOINT ["/usr/local/bin/decco-operator"]
