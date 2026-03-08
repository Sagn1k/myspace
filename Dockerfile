# Build stage
FROM golang:1.26-alpine AS builder

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o portfolio ./cmd/server/

# Runtime stage
FROM gcr.io/distroless/static-debian12

WORKDIR /app

COPY --from=builder /build/portfolio .
COPY --from=builder /build/templates ./templates
COPY --from=builder /build/static ./static
COPY --from=builder /build/content ./content

EXPOSE 8080

CMD ["/app/portfolio"]
