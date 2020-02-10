FROM golang:1.13.0-stretch AS builder

ENV GO111MODULE=on

WORKDIR /build

# Let's cache modules retrieval - those don't change so often
COPY go.mod .
COPY go.sum .
RUN go mod download

# Copy the code necessary to build the application
# You may want to change this to copy only what you actually need.
COPY . .

# Make dir for storing binaries
RUN mkdir /artifacts

# Build the application
RUN CGO_ENABLED=0 GOOS=linux go build -a -tags netgo -ldflags '-w' -o /artifacts/decco-operator ./cmd/operator

# Create the minimal runtime image
FROM alpine

WORKDIR /
COPY --from=builder /artifacts /
ENTRYPOINT ["/decco-operator"]
