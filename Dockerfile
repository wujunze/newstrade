FROM golang:1.24-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Static, stripped binary so it runs on a minimal image without libc surprises.
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o newstrade .

FROM alpine:3.19
RUN apk add --no-cache ca-certificates \
 && adduser -D -u 10001 appuser
WORKDIR /app
COPY --from=builder /app/newstrade .
USER appuser
EXPOSE 8080
CMD ["./newstrade"]
