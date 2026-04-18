FROM golang:1.23-alpine AS builder
WORKDIR /app
COPY . .
RUN CGO_ENABLED=0 go build -mod=vendor -o tvproxy-streams ./cmd/tvproxy-streams/

FROM alpine:3.19
RUN apk add --no-cache ffmpeg
COPY --from=builder /app/tvproxy-streams /usr/local/bin/
EXPOSE 8090
ENTRYPOINT ["tvproxy-streams"]
