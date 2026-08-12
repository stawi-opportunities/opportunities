# Build from monorepo root: docker build -f apps/calendar/Dockerfile .
FROM golang:1.26-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /calendar ./apps/calendar/cmd

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /calendar /calendar
COPY apps/calendar/migrations /migrations
ENV CALENDAR_MIGRATION_PATH=/migrations/0001
USER nonroot:nonroot
EXPOSE 8096
ENTRYPOINT ["/calendar"]
