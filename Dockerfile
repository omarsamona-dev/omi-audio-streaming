FROM golang:1.22.1-alpine AS builder
WORKDIR /app
COPY . .
# Pure-stdlib app; CGO off guarantees a static binary that runs on alpine.
ENV CGO_ENABLED=0
RUN go build -o ./omi-audio-streaming ./main.go


FROM alpine:latest AS runner
WORKDIR /app
# wget (busybox) is used by the compose healthcheck against /healthz.
COPY --from=builder /app/omi-audio-streaming .
EXPOSE 8080
ENTRYPOINT ["./omi-audio-streaming"]
