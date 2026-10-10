# syntax=docker/dockerfile:1
FROM golang:1.25-alpine AS development
WORKDIR /src
RUN go install github.com/air-verse/air@v1.61.7
COPY go.mod go.sum ./
RUN go mod download
COPY .air.toml ./
COPY cmd ./cmd
COPY internal ./internal
CMD ["air", "-c", ".air.toml"]

FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/web ./cmd/web

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/web /web
EXPOSE 8080
ENTRYPOINT ["/web"]
