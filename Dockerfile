FROM golang:1.25.5-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/awarddesk .

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates && rm -rf /var/lib/apt/lists/*
WORKDIR /app
COPY --from=build /out/awarddesk /app/awarddesk
ENV AWARDDESK_LISTEN_HOST=0.0.0.0
ENV AWARDDESK_DB=/data/awarddesk.db
CMD ["/app/awarddesk"]
