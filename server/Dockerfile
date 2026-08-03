# syntax=docker/dockerfile:1

FROM golang:1.22-alpine AS build
WORKDIR /src
RUN apk add --no-cache ca-certificates
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ENV CGO_ENABLED=0 GOOS=linux
RUN go build -trimpath -ldflags="-s -w" -o /out/seer-server ./cmd/seer-server

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /
COPY --from=build /out/seer-server /seer-server
USER nonroot:nonroot
EXPOSE 8080
ENV SEER_HTTP_ADDR=:8080
ENV SEER_DB_PATH=/data/seer.db
VOLUME ["/data"]
ENTRYPOINT ["/seer-server"]
