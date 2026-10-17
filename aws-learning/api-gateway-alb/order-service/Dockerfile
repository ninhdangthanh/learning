FROM golang:1.23-alpine AS builder
WORKDIR /src
COPY go.mod ./
COPY main.go ./
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/server main.go

FROM alpine:3.20
RUN apk add --no-cache curl ca-certificates
COPY --from=builder /out/server /server
EXPOSE 8080
ENTRYPOINT ["/server"]
