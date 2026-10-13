# codemode-worker alone: no shell, no libc, nothing to pivot to after an escape.
# docker build -f deploy/worker.Dockerfile -t codemode-worker .
FROM golang:1.27 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags=-s -o /codemode-worker ./cmd/codemode-worker

FROM scratch
COPY --from=build /codemode-worker /codemode-worker
USER 65534:65534
ENTRYPOINT ["/codemode-worker"]
