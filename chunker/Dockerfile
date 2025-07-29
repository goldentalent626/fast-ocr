FROM golang:1.24.2-bookworm as builder
WORKDIR /app/
COPY go.* /app/
RUN go mod download
COPY . /app/
RUN CGO_ENABLED=0 go build -v -o server

FROM gcr.io/distroless/static@sha256:72924583773eeeb9a6200e9f6dbfd95a27fbf25d39bfe7062c46d2654628f007
COPY --from=builder /app/server /app/server
CMD ["/app/server"] 
