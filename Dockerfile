FROM golang:1.23-bookworm AS go-builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/eds-analyser ./cmd/server

FROM node:22-bookworm-slim

ENV NODE_ENV=production
RUN apt-get update \
    && apt-get install -y --no-install-recommends chromium ca-certificates \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci --include=dev --omit=optional \
    && npm cache clean --force
COPY --from=go-builder /out/eds-analyser ./eds-analyser

RUN mkdir -p /app/.data
EXPOSE 10000
CMD ["./eds-analyser"]
