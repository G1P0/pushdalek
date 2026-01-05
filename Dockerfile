ARG GO_VERSION=1.24

FROM golang:${GO_VERSION}-alpine AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Сборка основного бота
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath -ldflags="-s -w" -o /out/pushdalek ./cmd/bot

# --- runtime ---
FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata \
  && adduser -D -H app

WORKDIR /app
COPY --from=build /out/pushdalek /app/pushdalek

# база по умолчанию в volume
ENV DB_PATH=/data/bot.db
VOLUME ["/data"]

USER app
ENTRYPOINT ["/app/pushdalek"]
