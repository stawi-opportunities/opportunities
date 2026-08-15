# Build from monorepo root: docker build -f apps/ats/Dockerfile .
FROM golang:1.26-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /ats ./apps/ats/cmd

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /ats /ats
COPY apps/ats/migrations /migrations
ENV ATS_MIGRATION_PATH=/migrations/0001
USER nonroot:nonroot
EXPOSE 8095
ENTRYPOINT ["/ats"]
