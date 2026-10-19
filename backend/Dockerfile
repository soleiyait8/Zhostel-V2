# One image, three programs: api, adms, migrate. Compose picks which to run.
FROM golang:1.24-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download && go mod verify
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/ ./cmd/api ./cmd/adms ./cmd/migrate ./cmd/healthcheck

# Distroless: no shell, no package manager, runs as an unprivileged user.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/ /usr/local/bin/
USER nonroot:nonroot
ENTRYPOINT []
CMD ["/usr/local/bin/api"]
