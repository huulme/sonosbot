FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/sonosbot ./cmd/sonosbot

FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata && adduser -D -H sonosbot && mkdir /data && chown sonosbot /data
USER sonosbot
WORKDIR /data
ENV SONOSBOT_DB=/data/sonosbot.db
COPY --from=build /out/sonosbot /usr/local/bin/sonosbot
ENTRYPOINT ["sonosbot"]
