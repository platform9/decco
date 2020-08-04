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
RUN stat decco-operator || CGO_ENABLED=0 go build -o decco-operator ./cmd/operator

FROM alpine:3.12

RUN apk --no-cache add ca-certificates

COPY --from=builder /app/decco-operator /

ENTRYPOINT ["/decco-operator"]