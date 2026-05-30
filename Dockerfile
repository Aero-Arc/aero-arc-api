FROM golang:1.24 AS build

WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/aero-arc-api ./cmd/aero-arc-api

FROM gcr.io/distroless/static-debian12
COPY --from=build /out/aero-arc-api /aero-arc-api
EXPOSE 8080
ENTRYPOINT ["/aero-arc-api"]
