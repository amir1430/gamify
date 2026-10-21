# The sample brand binary (cmd/gamify). cgo: the TigerBeetle client links a glibc static library,
# so build and run on Debian, not Alpine.
FROM golang:1.27.1-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -o /gamify ./cmd/gamify

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates wget && rm -rf /var/lib/apt/lists/*
COPY --from=build /gamify /usr/local/bin/gamify
WORKDIR /app
# gamify.yaml is mounted at /app/gamify.yaml; secrets come from env.
EXPOSE 8080 9090 10001
HEALTHCHECK --interval=5s --timeout=3s --retries=20 CMD wget -qO- http://localhost:8080/readyz || exit 1
CMD ["gamify"]
